package http1

import (
	"strings"
)

func ParseRequest(req string) Request {
	request := Request{
		HttpVersion: defaultHTTPVersion,
		Headers:     []Header{},
		Params:      make(map[string]string),
	}

	headerEnd := strings.Index(req, "\r\n\r\n")
	delimiterLength := 4

	if headerEnd < 0 {
		headerEnd = strings.Index(req, "\n\n")
		delimiterLength = 2
	}

	if headerEnd < 0 {
		return request
	}

	lines := strings.Split(
		strings.ReplaceAll((req)[:headerEnd], "\r\n", "\n"),
		"\n",
	)

	if len(lines) == 0 {
		return request
	}

	requestLine := strings.Fields(lines[0])

	if len(requestLine) != 3 {
		return request
	}

	request.Method = requestLine[0]
	request.HttpVersion = requestLine[2]

	// Parse path and optional query parameters.
	path, query, hasQuery := strings.Cut(requestLine[1], "?")

	request.Path = path

	if hasQuery && query != "" {
		for _, param := range strings.Split(query, "&") {
			key, value, hasValue := strings.Cut(param, "=")

			if key == "" {
				continue
			}

			if !hasValue {
				value = ""
			}

			request.Params[key] = value
		}
	}

	// Parse headers.
	for i := 1; i < len(lines); i++ {
		headerLine := lines[i]

		headerParts := strings.SplitN(headerLine, ":", 2)

		if len(headerParts) != 2 || strings.TrimSpace(headerParts[0]) == "" {
			continue
		}

		request.Headers = append(request.Headers, Header{
			Name:  strings.TrimSpace(headerParts[0]),
			Value: strings.TrimSpace(headerParts[1]),
		})
	}

	request.Body = []byte((req)[headerEnd+delimiterLength:])

	return request
}
