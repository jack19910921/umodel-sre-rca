package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/httpapi"
	"github.com/jack/umodel-sre-rca/internal/store"
)

func main() {
	configPath := flag.String("config", "/etc/sre-rca/sre.yaml", "path to runtime configuration")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if err := validateGatewayConfig(cfg); err != nil {
		log.Fatal(err)
	}
	repo, err := store.Open(cfg.StateDB)
	if err != nil {
		log.Fatal(err)
	}
	defer repo.Close()
	log.Printf("sre-gateway listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, httpapi.NewGateway(cfg.CallbackToken, cfg.CallbackCaptureDir, repo)))
}

func validateGatewayConfig(cfg config.Config) error {
	if cfg.CallbackToken == "" {
		return fmt.Errorf("callback_token is required")
	}
	return nil
}
