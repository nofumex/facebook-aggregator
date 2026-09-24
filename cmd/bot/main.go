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
	"github.com/egori/facebook-aggregator/internal/workers"
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
	uiMaxConns, backgroundMaxConns := poolPartition(cfg.DBMaxConns, cfg.DBBackgroundMaxConns)
	store, e := storage.Open(ctx, cfg.DatabaseURL, uiMaxConns, min(cfg.DBMinConns, uiMaxConns))
	if e != nil {
		fatal(e)
	}
	defer store.Close()
	backgroundStore := store
	if backgroundMaxConns > 0 {
		backgroundStore, e = storage.Open(ctx, cfg.DatabaseURL, backgroundMaxConns, 0)
		if e != nil {
			fatal(e)
		}
		defer backgroundStore.Close()
	}
	log.Info("database pools ready", "total_max_conns", cfg.DBMaxConns, "ui_max_conns", uiMaxConns, "background_max_conns", backgroundMaxConns)
	if e = migrations.Up(ctx, store.DB); e != nil {
		fatal(e)
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
	cookies := groups.Cookies{SB: cfg.Facebook.SB, DATR: cfg.Facebook.DATR, CUser: cfg.Facebook.CUser, XS: cfg.Facebook.XS, FR: cfg.Facebook.FR, PSL: cfg.Facebook.PSL, PSN: cfg.Facebook.PSN}
	if cipher != nil {
		if encoded, secretErr := store.Secret(ctx, cipher, "facebook.cookies"); secretErr == nil {
			if stored, decodeErr := fb.UnmarshalCookies(encoded); decodeErr == nil {
				cookies = stored
			} else {
				log.Warn("stored Facebook cookies ignored", "error", decodeErr)
			}
		}
	}
	adapterConfig := fb.TeslaShibeConfig{Cookies: cookies, MinRequestGap: cfg.Facebook.MinRequestGap, MaxRetries: cfg.Facebook.MaxRetries, DisableHTTP2: cfg.Facebook.DisableHTTP2, DocIDs: cfg.Facebook.DocIDs}
	var initialAdapter fb.Adapter
	initialAuthenticationFailure := false
	client, e := fb.NewTeslaShibe(adapterConfig)
	if e != nil {
		initialAuthenticationFailure = errors.Is(e, fb.ErrAuthentication)
		log.Error("facebook adapter unavailable", "error", e)
		initialAdapter = fb.UnavailableAdapter{Reason: e}
	} else {
		initialAdapter = client
		log.Info("facebook adapter ready", "adapter", initialAdapter.Name())
	}
	adapter := fb.NewDynamicAdapter(initialAdapter)
	api := tg.NewClient(cfg.TelegramToken)
	var bot *tg.Bot
	provider := func(c context.Context) llm.Provider {
		if bot == nil {
			return llm.Disabled{}
		}
		return bot.Provider(c)
	}
	extractor := enrichment.New(cfg.Extraction, backgroundStore, log)
	rankEngine := ranking.NewWithConfig(backgroundStore.RankingConfig(ctx))
	syncService := syncer.New(backgroundStore, adapter, parser.New(), rankEngine, extractor, log, cfg.WorkerConcurrency)
	collectionService := collections.NewWithRanking(backgroundStore, func(c context.Context) llm.Provider {
		return provider(c)
	}, rankEngine, log)
	bot = tg.NewBot(api, store, syncService, adapter, collectionService, cfg.AdminIDs, cipher, log, cfg.DefaultPoll)
	bot.SetFacebookCookieUpdater(func(updateCtx context.Context, raw string) (int, error) {
		if cipher == nil {
			return 0, fmt.Errorf("SETTINGS_ENCRYPTION_KEY is required")
		}
		newCookies, parseErr := fb.ParseCookies(raw)
		if parseErr != nil {
			return 0, parseErr
		}
		candidateConfig := adapterConfig
		candidateConfig.Cookies = newCookies
		candidate, createErr := fb.NewTeslaShibe(candidateConfig)
		if createErr != nil {
			return 0, createErr
		}
		checkCtx, cancel := context.WithTimeout(updateCtx, 45*time.Second)
		enabledGroups, groupsErr := backgroundStore.EnabledGroups(checkCtx)
		if groupsErr != nil {
			cancel()
			return 0, groupsErr
		}
		if len(enabledGroups) > 0 {
			group := enabledGroups[0]
			if group.FacebookID != "" {
				if checkErr := candidate.Check(checkCtx, group.FacebookID); checkErr != nil {
					cancel()
					return 0, checkErr
				}
			} else if _, _, _, resolveErr := candidate.ResolveGroup(checkCtx, group.URL); resolveErr != nil {
				cancel()
				return 0, resolveErr
			}
		}
		cancel()
		encoded, encodeErr := fb.MarshalCookies(newCookies)
		if encodeErr != nil {
			return 0, encodeErr
		}
		if saveErr := store.SetSecret(updateCtx, cipher, "facebook.cookies", encoded); saveErr != nil {
			return 0, saveErr
		}
		adapter.Replace(candidate)
		forceCtx, cancelForce := context.WithTimeout(updateCtx, 15*time.Second)
		defer cancelForce()
		queued, forceErr := syncService.ForceSyncAll(forceCtx, 50)
		if forceErr != nil {
			return 0, forceErr
		}
		return queued, nil
	})
	adapter.SetAuthenticationHandler(bot.NotifyFacebookAuthentication)
	if initialAuthenticationFailure {
		bot.NotifyFacebookAuthentication(fb.ErrAuthentication)
	}
	server := healthServer(cfg.HTTPAddr, store)
	go func() {
		log.Info("health server listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server", "error", err)
			stop()
		}
	}()
	go syncService.Run(ctx)
	go workers.RunExtractionRetry(ctx, backgroundStore, extractor, rankEngine, cfg, log)
	go workers.RunReranking(ctx, backgroundStore, rankEngine, cfg, log)
	go collectionService.Run(ctx, cfg.CollectionRefreshInterval, cfg.CollectionRefreshTimeout)
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

func poolPartition(total, requestedBackground int) (ui, background int) {
	if total < 2 {
		return 1, 0
	}
	background = requestedBackground
	if background < 1 {
		background = 1
	}
	if background > total-1 {
		background = total - 1
	}
	return total - background, background
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
