package lease_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/lease"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

func TestOnlyOneHolderAndFailover(t *testing.T) {
	url := pgtest.NewURL(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var running, overlap, starts atomic.Int32
	work := func(ctx context.Context) error {
		if running.Add(1) > 1 {
			overlap.Add(1)
		}
		starts.Add(1)
		<-ctx.Done()
		running.Add(-1)
		return nil
	}

	const name = "telegram:poller"
	managers := make([]*lease.Manager, 3)
	cancels := make([]context.CancelFunc, 3)
	var wg sync.WaitGroup
	for i := range managers {
		d, err := db.Open(ctx, config.DatabaseConfig{DSN: url, MaxConns: 4}, pgtest.Logger())
		require.NoError(t, err)
		t.Cleanup(d.Close)
		m := lease.NewManager(d.Pool, pgtest.Logger())
		m.RetryInterval = 100 * time.Millisecond
		m.CheckInterval = 100 * time.Millisecond
		managers[i] = m
		rctx, rcancel := context.WithCancel(ctx)
		cancels[i] = rcancel
		wg.Add(1)
		go func() { defer wg.Done(); m.Run(rctx, name, work) }()
	}
	holder := func() int {
		for i, m := range managers {
			if m.Held(name) {
				return i
			}
		}
		return -1
	}
	stopped := map[int]bool{}
	for round := 1; round <= 3; round++ {
		require.Eventually(t, func() bool { return running.Load() == 1 && holder() >= 0 && !stopped[holder()] }, 5*time.Second, 20*time.Millisecond)
		require.Equal(t, int32(round), starts.Load())
		h := holder()
		cancels[h]()
		stopped[h] = true
		require.Eventually(t, func() bool { return !managers[h].Held(name) }, 5*time.Second, 20*time.Millisecond)
	}
	wg.Wait()
	require.Zero(t, overlap.Load(), "two replicas ran the singleton at once")
	require.Zero(t, running.Load())
}
