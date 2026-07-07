package main

import (
	"log"
	"net/http"

	"github.com/0xFredZhang/Hermes/internal/api"
	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/config"
	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cipher, err := crypto.NewCipher(cfg.MasterKey)
	if err != nil {
		log.Fatalf("cipher: %v", err)
	}
	st, err := store.Open(cfg.DBPath, cipher)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	renderer, err := web.NewRenderer()
	if err != nil {
		log.Fatalf("renderer: %v", err)
	}

	deps := api.Deps{
		Store:     st,
		Validator: cloud.NewValidator(),
		Auth:      auth.New(cfg.LoginPassword, cfg.MasterKey),
		Renderer:  renderer,
	}
	log.Printf("hermes listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, api.NewRouter(deps)); err != nil {
		log.Fatal(err)
	}
}
