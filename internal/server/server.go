package server

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/generalpy101/pygoserver/internal/content"
	"github.com/generalpy101/pygoserver/internal/handler"
	"github.com/generalpy101/pygoserver/internal/http1"
	"github.com/generalpy101/pygoserver/internal/store"
)

func ListenAndServe(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", address, err)
	}
	defer listener.Close()

	log.Printf("listening on %s", listener.Addr())

	fileStore := content.OpenFileStore("public")
	dataStore := store.New()
	persistenceEngine := store.NewPersistenceEngine("./", dataStore)
	err = persistenceEngine.LoadData()
	if err != nil {
		return err
	}
	go persistenceEngine.StartPersistence()
	defer persistenceEngine.StopPersistence()
	templateEngine, err := content.NewEngine("./internal/templates")
	defer fileStore.CloseFileStore()

	if err != nil {
		return err
	}

	for {
		connection, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept connection: %w", err)
		}
		go handleConnection(connection, fileStore, dataStore, templateEngine)
	}
}

func handleConnection(connection net.Conn, fileStore *content.FileStore, dataStore *store.Store, templateEngine *content.Engine) {
	defer connection.Close()

	rawRequest, err := readRequest(bufio.NewReader(connection))
	if err != nil {
		log.Printf("read request from %s: %v", connection.RemoteAddr(), err)
		return
	}

	request := http1.ParseRequest(rawRequest)
	// Note: no per-request log line here on purpose. Logging every
	// request writes synchronously to the terminal behind a mutex,
	// which roughly halved throughput under load in benchmarks.
	// Use the monitor dashboard (localhost:6060/dashboard) instead.
	response := handler.HandleRequest(request, fileStore, dataStore, templateEngine)
	if _, err := io.WriteString(connection, http1.FormatResponse(response)); err != nil {
		log.Printf("write response to %s: %v", connection.RemoteAddr(), err)
	}
}

func readRequest(reader *bufio.Reader) (string, error) {
	var rawRequest strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read request headers: %w", err)
		}
		rawRequest.WriteString(line)
		if rawRequest.Len() > 1<<20 {
			return "", fmt.Errorf("request headers exceed 1 MiB")
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	request := http1.ParseRequest(rawRequest.String())
	contentLength := 0
	for _, header := range request.Headers {
		if !strings.EqualFold(header.Name, "Content-Length") {
			continue
		}
		parsedLength, err := strconv.Atoi(header.Value)
		if err != nil || parsedLength < 0 || parsedLength > 1<<20-rawRequest.Len() {
			return "", fmt.Errorf("invalid Content-Length %q", header.Value)
		}
		contentLength = parsedLength
		break
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return "", fmt.Errorf("read request body: %w", err)
	}
	rawRequest.Write(body)
	return rawRequest.String(), nil
}
