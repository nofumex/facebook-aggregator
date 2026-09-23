package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/egori/facebook-aggregator/internal/config"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/enrichment"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/storage"
	"github.com/egori/facebook-aggregator/internal/workers"
	"github.com/egori/facebook-aggregator/migrations"
	"github.com/joho/godotenv"
)

func main() {
	batch := flag.Int("batch", 100, "listings per resumable batch")
	flag.Parse()
	_ = godotenv.Load()
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	if !cfg.Extraction.Enabled {
		fatal(fmt.Errorf("LLM_EXTRACTION_ENABLED must be true"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	store, err := storage.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBMinConns)
	if err != nil {
		fatal(err)
	}
	defer store.Close()
	if err = migrations.Up(ctx, store.DB); err != nil {
		fatal(err)
	}
	extractor := enrichment.New(cfg.Extraction, store, log)
	engine := ranking.NewWithConfig(store.RankingConfig(ctx))
	var cursor int64
	for {
		items, e := store.ExtractionBackfillBatch(ctx, cfg.Extraction.SchemaVersion, cursor, *batch)
		if e != nil {
			fatal(e)
		}
		if len(items) == 0 {
			break
		}
		var failures atomic.Int64
		for _, l := range items {
			if l.ID > cursor {
				cursor = l.ID
			}
		}
		workers.ProcessBounded(ctx, items, cfg.BackfillConcurrency, func(_ context.Context, l domain.Listing) {
			out, _, extractErr := extractor.Apply(ctx, l)
			b, benchErr := store.Benchmarks(ctx, out)
			if benchErr == nil {
				out.DealScore, out.ScoreConfidence = engine.Score(out, b, time.Now())
			}
			if extractErr != nil {
				out.LastExtractionError = extractErr.Error()
				failures.Add(1)
				log.Warn("backfill extraction failed", "listing_id", l.ID, "error", extractErr)
			}
			if e := store.UpdateExtractedListing(ctx, out); e != nil {
				failures.Add(1)
				log.Error("backfill save failed", "listing_id", l.ID, "error", e)
			}
		})
		log.Info("backfill batch complete", "cursor", cursor, "processed", len(items), "failures", failures.Load())
	}
	log.Info("backfill complete", "schema_version", cfg.Extraction.SchemaVersion)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
