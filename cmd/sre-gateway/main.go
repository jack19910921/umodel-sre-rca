package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/jack/umodel-sre-rca/internal/config"
)

func main() {
	configPath := flag.String("config", "/etc/sre-rca/sre.yaml", "path to runtime configuration")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// CloudMonitor ingestion is deliberately registered only after a redacted
	// real callback fixture establishes its exact field map.
	log.Printf("sre-gateway listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
}
