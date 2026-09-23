package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/egori/facebook-aggregator/internal/collections"
	"github.com/egori/facebook-aggregator/internal/config"
	"github.com/egori/facebook-aggregator/internal/enrichment"
	fb "github.com/egori/facebook-aggregator/internal/facebook"
	"github.com/egori/facebook-aggregator/internal/llm"
	"github.com/egori/facebook-aggregator/internal/parser"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/secrets"
	"github.com/egori/facebook-aggregator/internal/storage"
	"github.com/egori/facebook-aggregator/internal/syncer"
	tg "github.com/egori/facebook-aggregator/internal/telegram"
	"github.com/egori/facebook-aggregator/migrations"
	"github.com/joho/godotenv"
	"github.com/teslashibe/facebook-go/groups"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	_ = godotenv.Load()
	cfg, e := config.Load()
	if e != nil {
		fatal(e)
	}
	if cfg.TelegramToken == "" {
		fatal(errors.New("TELEGRAM_BOT_TOKEN is required"))
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, e := storage.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		fatal(e)
	}
	defer store.Close()
	if e = migrations.Up(ctx, store.DB); e != nil {
		fatal(e)
	}
	var adapter fb.Adapter
	client, e := fb.NewTeslaShibe(fb.TeslaShibeConfig{Cookies: groups.Cookies{SB: cfg.Facebook.SB, DATR: cfg.Facebook.DATR, CUser: cfg.Facebook.CUser, XS: cfg.Facebook.XS, FR: cfg.Facebook.FR, PSL: cfg.Facebook.PSL, PSN: cfg.Facebook.PSN}, MinRequestGap: cfg.Facebook.MinRequestGap, MaxRetries: cfg.Facebook.MaxRetries, DocIDs: cfg.Facebook.DocIDs})
	if e != nil {
		log.Error("facebook adapter unavailable", "error", e)
		adapter = fb.UnavailableAdapter{Reason: e}
	} else {
		adapter = client
		log.Info("facebook adapter ready", "adapter", adapter.Name())
	}
	var cipher *secrets.Cipher
	if len(cfg.EncryptionKey) > 0 {
		cipher, e = secrets.New(cfg.EncryptionKey)
		if e != nil {
			fatal(e)
		}
	} else {
		log.Warn("SETTINGS_ENCRYPTION_KEY missing; secret updates in Telegram admin are disabled")
	}
	api := tg.NewClient(cfg.TelegramToken)
	var bot *tg.Bot
	provider := func(c context.Context) llm.Provider {
		if bot == nil {
			return llm.Disabled{}
		}
		return bot.Provider(c)
	}
	extractor := enrichment.New(cfg.Extraction, store, log)
	rankEngine := ranking.NewWithConfig(store.RankingConfig(ctx))
	syncService := syncer.New(store, adapter, parser.New(), rankEngine, extractor, log, cfg.WorkerConcurrency)
	collectionService := collections.NewWithRanking(store, func(c context.Context) llm.Provider {
		return provider(c)
	}, rankEngine, log)
	bot = tg.NewBot(api, store, syncService, adapter, collectionService, cfg.AdminIDs, cipher, log, cfg.DefaultPoll)
	server := healthServer(cfg.HTTPAddr, store)
	go func() {
		log.Info("health server listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server", "error", err)
			stop()
		}
	}()
	go syncService.Run(ctx)
	go func() {
		if err := bot.Run(ctx); err != nil {
			log.Error("telegram bot stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	log.Info("shutdown complete")
}
func healthServer(addr string, s *storage.Store) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.DB.Ping(c); err != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ready\n"))
	})
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}
func fatal(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
