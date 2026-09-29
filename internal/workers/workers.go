package workers

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/nofumex/telegram-aggregator/internal/config"
	"github.com/nofumex/telegram-aggregator/internal/domain"
	"github.com/nofumex/telegram-aggregator/internal/ranking"
	"github.com/nofumex/telegram-aggregator/internal/storage"
)

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
