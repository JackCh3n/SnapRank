package main

import (
	"context"
	"fmt"
	"os"

	"snaprank/internal/config"
	"snaprank/internal/provider"
)

func main() {
	cfg := config.Default()
	cfg.Provider.Type = "tokenrhythm"
	cfg.Provider.Protocol = "anthropic"
	cfg.Provider.BaseURL = "http://127.0.0.1:9999/api/coding"
	if k := os.Getenv("ARK_KEY"); k != "" {
		cfg.Provider.APIKey = k
	}

	p, err := provider.New(cfg)
	if err != nil {
		fmt.Println("New:", err)
		return
	}
	ids, err := p.ListModels(context.Background())
	fmt.Println("result:", len(ids), ids)
}
