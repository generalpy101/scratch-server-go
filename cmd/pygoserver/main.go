package main

import (
	"log"

	"github.com/generalpy101/pygoserver/internal/server"
)

func main() {
	server.StartMonitor("localhost:6060")

	err := server.ListenAndServe("localhost:8080")

	if err != nil {
		log.Panicf("Failed to start server\nError: %s", err.Error())
	}
}
