package handler

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"github.com/generalpy101/pygoserver/internal/content"
	"github.com/generalpy101/pygoserver/internal/http1"
	"github.com/generalpy101/pygoserver/internal/store"
)

func HandleRequest(req http1.Request, fileStore *content.FileStore, dataStore *store.Store, templateEngine *content.Engine) http1.Response {
	resp := http1.Response{}

	if req.Path == "/" {
		resp.Status = http1.StatusOK
		handlePublicFiles("index.html", &resp, fileStore)
	} else if strings.HasPrefix(req.Path, "/assets") {
		resp.Status = http1.StatusOK
		handlePublicFiles(req.Path, &resp, fileStore)
	} else if strings.HasPrefix(req.Path, "/api") {
		HandleAPI(req, &resp, dataStore)
	} else if strings.HasPrefix(req.Path, "/todos") {
		if req.Method != http1.HTTPGet {
			resp.Body = []byte(Return400("Only GET is allowed"))
			resp.Status = 400
			return resp
		}

		ctx := content.NewContext()

		// Which page are we on? /todos?page=2, default is page 1.
		page := 1
		if value, err := strconv.Atoi(req.Params["page"]); err == nil && value > 0 {
			page = value
		}

		const todosPerPage = 10

		todos, total := dataStore.GetPage("todo", page, todosPerPage)
		pages := (total + todosPerPage - 1) / todosPerPage

		// If the page is past the end (e.g. todos were deleted),
		// show the last page instead of an empty one.
		if pages > 0 && page > pages {
			page = pages
			todos, _ = dataStore.GetPage("todo", page, todosPerPage)
		}

		// Decode every todo's JSON data into a map so the template
		// can use member access ({{ todo.title }}) and loops
		// ({{ for todo in todos }}).
		todoList := make([]any, 0, len(todos))
		for _, todo := range todos {
			var data map[string]any
			if err := json.Unmarshal(todo.Data, &data); err != nil {
				continue
			}
			data["id"] = todo.ID
			todoList = append(todoList, data)
		}

		ctx.Values["todos"] = todoList
		ctx.Values["page"] = page
		ctx.Values["totalPages"] = pages
		// The template uses these to show/hide the prev/next buttons.
		ctx.Values["hasPrev"] = page > 1
		ctx.Values["hasNext"] = page < pages

		data, err := templateEngine.GetTemplate("todo.gotemp", ctx)

		if err != nil {
			resp.Body = []byte(Return500(err))
			resp.Status = 500
			return resp
		}

		resp.Body = []byte(data)
		resp.Status = 200

	} else {
		resp.Body = []byte(Return404())
		resp.Status = http1.StatusNotFound
	}

	return resp
}

func handleServerError(resp *http1.Response, err error) {
	log.Printf("Server Error %s", err.Error())
	resp.Body = []byte(Return500(err))
	resp.Status = http1.ServerError
}

func handlePublicFiles(path string, resp *http1.Response, filestore *content.FileStore) {
	relativePath := strings.TrimPrefix(path, "/")
	content, err := filestore.Read(relativePath)
	if err != nil {
		handleServerError(resp, err)
		return
	}

	resp.Body = content
}
