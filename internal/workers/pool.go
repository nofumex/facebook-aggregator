package workers

import (
	"context"
	"sync"
)

// ProcessBounded creates exactly concurrency workers, never one goroutine per item.
func ProcessBounded[T any](ctx context.Context, items []T, concurrency int, fn func(context.Context, T)) {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}
	if concurrency == 0 {
		return
	}
	jobs := make(chan T)
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for range concurrency {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-jobs:
					if !ok {
						return
					}
					fn(ctx, item)
				}
			}
		}()
	}
	for _, item := range items {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()
}
