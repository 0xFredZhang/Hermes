package main

import (
	"log"
	"net/http"

	"github.com/0xFredZhang/Hermes/internal/api"
)

func main() {
	addr := ":8080"
	log.Printf("hermes listening on %s", addr)
	if err := http.ListenAndServe(addr, api.NewRouter()); err != nil {
		log.Fatal(err)
	}
}
