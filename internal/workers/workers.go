package workers

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/egori/facebook-aggregator/internal/config"
	"github.com/egori/facebook-aggregator/internal/domain"
	"github.com/egori/facebook-aggregator/internal/enrichment"
	"github.com/egori/facebook-aggregator/internal/ranking"
	"github.com/egori/facebook-aggregator/internal/storage"
)

func RunExtractionRetry(ctx context.Context, store *storage.Store, extractor *enrichment.Service, engine ranking.Engine, cfg config.Config, log *slog.Logger) {
	if !cfg.Extraction.Enabled {
		return
	}
	run := func() {
		items, err := store.ExtractionRetryBatch(ctx, cfg.Extraction.SchemaVersion, cfg.ExtractionRetryBatch)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("load extraction retries", "error", err)
			}
			return
		}
		var processed, successes, failures, skipped atomic.Int64
		ProcessBounded(ctx, items, cfg.BackfillConcurrency, func(c context.Context, l domain.Listing) {
			processed.Add(1)
			out, enriched, extractErr := extractor.Apply(c, l)
			if extractErr != nil {
				out.LastExtractionError = extractErr.Error()
				failures.Add(1)
			} else if enriched {
				successes.Add(1)
			} else {
				skipped.Add(1)
			}
			if b, e := store.Benchmarks(c, out); e == nil {
				out.DealScore, out.ScoreConfidence = engine.Score(out, b, time.Now())
			}
			if e := store.UpdateExtractedListing(c, out); e != nil {
				failures.Add(1)
				log.Warn("save extraction retry", "listing_id", l.ID, "error", e)
			}
		})
		log.Info("extraction retry batch", "processed", processed.Load(), "success", successes.Load(), "failed", failures.Load(), "skipped", skipped.Load())
	}
	runPeriodic(ctx, cfg.ExtractionRetryInterval, run)
}

func RunReranking(ctx context.Context, store *storage.Store, engine ranking.Engine, cfg config.Config, log *slog.Logger) {
	run := func() {
		items, err := store.StaleRankingBatch(ctx, time.Now().Add(-cfg.RerankInterval), cfg.RerankBatch)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("load stale rankings", "error", err)
			}
			return
		}
		var updated, failed atomic.Int64
		ProcessBounded(ctx, items, min(cfg.BackfillConcurrency, 2), func(c context.Context, l domain.Listing) {
			b, e := store.Benchmarks(c, l)
			if e != nil {
				failed.Add(1)
				return
			}
			score, confidence := engine.Score(l, b, time.Now())
			if e = store.UpdateScore(c, l.ID, score, confidence); e != nil {
				failed.Add(1)
			} else {
				updated.Add(1)
			}
		})
		log.Info("reranking batch", "selected", len(items), "updated", updated.Load(), "failed", failed.Load())
	}
	runPeriodic(ctx, cfg.RerankInterval, run)
}

func runPeriodic(ctx context.Context, interval time.Duration, fn func()) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	timer := time.NewTimer(min(interval, 10*time.Second))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			fn()
			timer.Reset(interval)
		}
	}
}
