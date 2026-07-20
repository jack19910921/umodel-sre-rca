package main

import (
	"flag"
	"log"

	"github.com/jack/umodel-sre-rca/internal/config"
)

func main() {
	configPath := flag.String("config", "/etc/sre-rca/sre.yaml", "path to runtime configuration")
	flag.Parse()
	if _, err := config.Load(*configPath); err != nil {
		log.Fatal(err)
	}
}
