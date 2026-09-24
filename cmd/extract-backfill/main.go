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
	limit := flag.Int("limit", 0, "maximum listings to process in total (0 = unlimited)")
	flag.Parse()
	if *batch < 1 {
		fatal(fmt.Errorf("-batch must be at least 1"))
	}
	if *limit < 0 {
		fatal(fmt.Errorf("-limit must be 0 or greater"))
	}
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
	processed := 0
	for {
		batchSize := nextBatchSize(*batch, *limit, processed)
		if batchSize == 0 {
			break
		}
		items, e := store.ExtractionBackfillBatch(ctx, cfg.Extraction.SchemaVersion, cursor, batchSize)
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
		processed += len(items)
		log.Info("backfill batch complete", "cursor", cursor, "processed", len(items), "failures", failures.Load())
	}
	log.Info("backfill complete", "schema_version", cfg.Extraction.SchemaVersion, "processed_total", processed, "limit", *limit)
}

func nextBatchSize(batch, limit, processed int) int {
	if limit == 0 {
		return batch
	}
	remaining := limit - processed
	if remaining <= 0 {
		return 0
	}
	if remaining < batch {
		return remaining
	}
	return batch
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
