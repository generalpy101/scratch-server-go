// Wait until the DOM is ready: this script is loaded in <head>, so the
// elements below don't exist yet while the browser is still parsing.
document.addEventListener("DOMContentLoaded", () => {
    const form = document.querySelector("#todo-form")
    const list = document.querySelector(".todo-list")

    //
    // Create: intercept the form submit, POST JSON to the API,
    // then reload the page so the new todo shows up.
    //
    if (form) {
        form.addEventListener("submit", async (event) => {
            // Stop the browser's normal form POST (which would send
            // form-encoded data, but our API expects JSON).
            event.preventDefault()

            const input = document.querySelector("#todo-title")
            const title = input.value.trim()

            if (title === "") {
                return
            }

            const response = await fetch("/api/todos", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ title: title, completed: false }),
            })

            if (response.ok) {
                window.location.reload()
            } else {
                alert("Failed to create todo: " + response.status)
            }
        })
    }

    //
    // Delete: one click listener on the whole list ("event delegation")
    // instead of one listener per button.
    //
    // The id is read from the todo's own .todo-id span, because our
    // template engine can't put {{ variables }} inside HTML attributes
    // (so a data-id="{{ todo.id }}" attribute would not work).
    //
    if (list) {
        list.addEventListener("click", async (event) => {
            const button = event.target.closest(".todo-delete")

            if (!button) {
                return
            }

            const item = button.closest(".todo-item")
            const id = item.querySelector(".todo-id").textContent.trim()

            const response = await fetch("/api/todos?id=" + encodeURIComponent(id), {
                method: "DELETE",
            })

            if (response.ok) {
                window.location.reload()
            } else {
                alert("Failed to delete todo: " + response.status)
            }
        })
    }

    //
    // Pagination: the current page number lives in the #page-current
    // span (our template engine can't put {{ variables }} inside HTML
    // attributes, so JS builds the URL instead of using an <a href>).
    // Navigating to /todos?page=N re-renders the page server-side.
    //
    const prev = document.querySelector("#page-prev")
    const next = document.querySelector("#page-next")
    const current = document.querySelector("#page-current")

    const goToPage = (page) => {
        window.location.href = "/todos?page=" + page
    }

    if (prev && current) {
        prev.addEventListener("click", () => goToPage(Number(current.textContent) - 1))
    }

    if (next && current) {
        next.addEventListener("click", () => goToPage(Number(current.textContent) + 1))
    }
})
