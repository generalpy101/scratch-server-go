package http1

const defaultHTTPVersion = "HTTP/1.1"

type Header struct {
	Name  string
	Value string
}

type Request struct {
	HttpVersion string
	Method      string
	Headers     []Header
	Path        string
	Body        []byte
	Params      map[string]string
}

type Response struct {
	Status  int
	Headers []Header
	Body    []byte
}

const (
	StatusOK               = 200
	StatusNotFound         = 404
	StatusMethodError      = 400
	StatusMethodNotAllowed = 405
	ServerError            = 500
)

const (
	HTTPGet    = "GET"
	HTTPPost   = "POST"
	HTTPDelete = "DELETE"
)
