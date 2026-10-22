package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"uuid"

	"github.com/generalpy101/pygoserver/internal/http1"
	"github.com/generalpy101/pygoserver/internal/store"
)

func HandleAPI(req http1.Request, resp *http1.Response, dataStore *store.Store) {
	if req.Path != "/api/todos" {
		writeJSONError(resp, http1.StatusNotFound, "API route not found")
		return
	}

	switch req.Method {
	case http1.HTTPGet:
		page, limit := parsePageParams(req)
		todos, total := dataStore.GetPage("todo", page, limit)

		// The response is always an "envelope": the page of items plus
		// the numbers a UI needs to render pagination controls.
		body := map[string]any{
			"items":       todos,
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": totalPages(total, limit),
		}
		if err := writeJSONResponse(resp, body); err != nil {
			writeJSONError(resp, http1.ServerError, "could not encode response")
		}
	case http1.HTTPPost:
		id := uuid.NewV4().String()
		item := store.Item{
			ID:   id,
			Kind: "todo",
			Data: req.Body,
		}
		// Store the raw todo payload as Data. Passing the whole Item
		// here would double-encode it: Data would contain a JSON copy
		// of the Item struct instead of just the todo payload.
		if _, err := dataStore.Put("todo", id, json.RawMessage(req.Body)); err != nil {
			log.Printf("Error writing todo to store: %s", err.Error())
		}
		if err := writeJSONResponse(resp, item); err != nil {
			writeJSONError(resp, http1.ServerError, "could not encode response")
		}
	case http1.HTTPDelete:
		todoId, exists := req.Params["id"]
		if exists == false {
			writeJSONError(resp, http1.StatusMethodError, "id is a required param")
			return
		}
		err := dataStore.Delete("todo", todoId)
		if err != nil {
			writeJSONError(resp, http1.ServerError, err.Error())
			return
		}
		if err := writeJSONResponse(resp, "{}"); err != nil {
			writeJSONError(resp, http1.ServerError, "could not encode response")
		}
	default:
		writeJSONError(resp, http1.StatusMethodNotAllowed, "method not allowed")
	}
}

// parsePageParams reads ?page= and ?limit= from the request, falling
// back to sane defaults: page 1, 20 items per page, at most 100.
func parsePageParams(req http1.Request) (page, limit int) {
	page = 1
	limit = 20

	if value, err := strconv.Atoi(req.Params["page"]); err == nil && value > 0 {
		page = value
	}

	if value, err := strconv.Atoi(req.Params["limit"]); err == nil && value > 0 {
		limit = min(value, 100)
	}

	return page, limit
}

// totalPages computes how many pages are needed to show total items
// with the given page size (rounding up).
func totalPages(total, limit int) int {
	return (total + limit - 1) / limit
}

func writeJSONResponse(resp *http1.Response, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal JSON response: %w", err)
	}

	resp.Status = http1.StatusOK
	resp.Body = body
	resp.Headers = append(resp.Headers,
		http1.Header{Name: "Content-Type", Value: "application/json"},
		http1.Header{Name: "Content-Length", Value: strconv.Itoa(len(body))},
	)

	return nil
}

func writeJSONError(resp *http1.Response, status int, message string) {
	if err := writeJSONResponse(resp, map[string]string{"error": message}); err != nil {
		resp.Status = status
		resp.Body = []byte(`{"error":"internal server error"}`)
		return
	}
	resp.Status = status
}
