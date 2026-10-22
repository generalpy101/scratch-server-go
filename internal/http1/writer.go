package http1

import (
	"fmt"
	"net/http"
	"strings"
)

func FormatResponse(resp Response) string {
	var response strings.Builder
	statusText := http.StatusText(resp.Status)
	if statusText == "" {
		statusText = "Unknown"
	}

	fmt.Fprintf(&response, "%s %d %s\r\n", defaultHTTPVersion, resp.Status, statusText)

	for _, header := range resp.Headers {
		fmt.Fprintf(&response, "%s: %s\r\n", header.Name, header.Value)
	}
	response.WriteString("\r\n")
	response.Write(resp.Body)

	return response.String()
}
