package workers

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchHundredUsesBoundedConcurrency(t *testing.T) {
	items := make([]int, 100)
	var active, maxActive atomic.Int64
	ProcessBounded(context.Background(), items, 2, func(context.Context, int) {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
	})
	if maxActive.Load() != 2 {
		t.Fatalf("max concurrency=%d", maxActive.Load())
	}
}
