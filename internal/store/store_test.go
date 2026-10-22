package store_test

import (
	"fmt"
	"testing"

	"github.com/generalpy101/pygoserver/internal/store"
)

func TestGetPage(t *testing.T) {
	dataStore := store.New()

	// Create 25 items so we get 3 pages of 10.
	for i := 0; i < 25; i++ {
		if _, err := dataStore.Put("todo", fmt.Sprintf("id-%02d", i), map[string]int{"n": i}); err != nil {
			t.Fatalf("There should be no error but there is with: %s", err.Error())
		}
	}

	// Page 1: 10 items, total 25.
	page1, total := dataStore.GetPage("todo", 1, 10)

	if total != 25 {
		t.Errorf("Expected total 25, got %d", total)
	}

	if len(page1) != 10 {
		t.Errorf("Expected 10 items on page 1, got %d", len(page1))
	}

	// Page 3: only 5 items left.
	page3, _ := dataStore.GetPage("todo", 3, 10)

	if len(page3) != 5 {
		t.Errorf("Expected 5 items on page 3, got %d", len(page3))
	}

	// Page past the end: empty, but not an error.
	page4, _ := dataStore.GetPage("todo", 4, 10)

	if len(page4) != 0 {
		t.Errorf("Expected 0 items on page 4, got %d", len(page4))
	}

	// Pages must not overlap and must keep a stable order across calls.
	page1Again, _ := dataStore.GetPage("todo", 1, 10)

	for i := range page1 {
		if page1[i].ID != page1Again[i].ID {
			t.Error("Same page returned different order on repeated calls")
			break
		}
	}

	page2, _ := dataStore.GetPage("todo", 2, 10)

	seen := make(map[string]bool)
	for _, item := range page1 {
		seen[item.ID] = true
	}
	for _, item := range page2 {
		if seen[item.ID] {
			t.Errorf("Item %q appears on both page 1 and page 2", item.ID)
		}
	}
}
