package main

import (
	"context"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/nofumex/telegram-aggregator/internal/config"
	"github.com/nofumex/telegram-aggregator/internal/storage"
	"github.com/nofumex/telegram-aggregator/migrations"
	"os"
)

func main() {
	_ = godotenv.Load()
	cfg, e := config.Load()
	if e != nil {
		fail(e)
	}
	ctx := context.Background()
	s, e := storage.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		fail(e)
	}
	defer s.Close()
	if e = migrations.Up(ctx, s.DB); e != nil {
		fail(e)
	}
	fmt.Println("migrations: up to date")
}
func fail(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
