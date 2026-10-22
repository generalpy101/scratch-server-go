package store

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

var ErrNotFound = errors.New("store: item not found")

type Store struct {
	mu    sync.RWMutex
	items map[key]Item

	// order holds the keys in insertion order. The map alone cannot
	// provide this: Go map iteration order is random. Pagination needs
	// a stable order, and keeping it in a slice is much cheaper than
	// sorting every item on every request.
	order []key
}

type key struct {
	Kind string `json:"kind"`
	Id   string `json:"id"`
}

type Item struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func New() *Store {
	return &Store{items: make(map[key]Item)}
}

func (s *Store) Put(kind, id string, value any) (string, error) {
	if kind == "" {
		return "", errors.New("store: kind must not be empty")
	}

	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("store: encode item: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items == nil {
		s.items = make(map[key]Item)
	}
	if id == "" {
		for {
			id, err = newID()
			if err != nil {
				return "", fmt.Errorf("store: generate id: %w", err)
			}
			if _, exists := s.items[key{Kind: kind, Id: id}]; !exists {
				break
			}
		}
	}

	k := key{Kind: kind, Id: id}
	now := time.Now().UTC()
	createdAt := now
	previous, exists := s.items[k]
	if exists {
		createdAt = previous.CreatedAt
	}

	s.items[k] = Item{
		ID:        id,
		Kind:      kind,
		Data:      data,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}

	// Only brand new keys extend the insertion order; overwriting an
	// existing item keeps its original position.
	if !exists {
		s.order = append(s.order, k)
	}

	return id, nil
}

func (s *Store) Get(kind, id string) (Item, error) {
	k := key{Kind: kind, Id: id}
	s.mu.RLock()
	item, exists := s.items[k]
	if exists {
		item.Data = append(json.RawMessage(nil), item.Data...)
	}
	s.mu.RUnlock()
	if !exists {
		return Item{}, fmt.Errorf("%w: %s/%s", ErrNotFound, kind, id)
	}

	return item, nil
}

func (s *Store) GetAll(kinds ...string) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	kindFilter := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		kindFilter[kind] = struct{}{}
	}

	items := make([]Item, 0, len(s.items))
	for _, item := range s.items {
		if len(kindFilter) > 0 {
			if _, matches := kindFilter[item.Kind]; !matches {
				continue
			}
		}
		item.Data = append(json.RawMessage(nil), item.Data...)
		items = append(items, item)
	}
	return items
}

// GetPage returns one page of items of the given kind plus the total
// number of items (across all pages). Items come in insertion order.
//
// page is 1-based: page 1 is the first page.
//
// This walks the insertion-order slice once and only copies the items
// that actually land on the requested page. The old version copied and
// sorted EVERY item on every request, which got slower and slower as
// the store grew (that was the benchmark's mixed-workload collapse).
func (s *Store) GetPage(kind string, page, limit int) ([]Item, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	start := (page - 1) * limit
	total := 0
	items := make([]Item, 0, limit)

	for _, k := range s.order {
		if k.Kind != kind {
			continue
		}

		// Collect items only once we reach the page's first item,
		// and stop collecting once the page is full.
		if total >= start && len(items) < limit {
			item := s.items[k]
			item.Data = append(json.RawMessage(nil), item.Data...)
			items = append(items, item)
		}

		total++
	}

	return items, total
}

func (s *Store) Delete(kind, id string) error {
	k := key{Kind: kind, Id: id}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[k]; !exists {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, kind, id)
	}
	delete(s.items, k)

	// Keep the insertion-order slice in sync. Deletes are rare
	// compared to reads, so a linear scan is fine here.
	for i, existing := range s.order {
		if existing == k {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}

	return nil
}

func (s *Store) GetOrderedItems() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Item, 0)

	for _, k := range s.order {
		val, exists := s.items[k]

		if !exists {
			log.Panicf("Data corruption alert, %s not found in store\n", k)
		}

		items = append(items, val)
	}

	return items
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}
