package pubsub_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/pubsub"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

func TestFanOutAcrossConnections(t *testing.T) {
	d := pgtest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := pubsub.NewListener(d.Pool, pgtest.Logger())
	b := pubsub.NewListener(d.Pool, pgtest.Logger())
	go a.Run(ctx)
	go b.Run(ctx)
	ca, ua := a.Subscribe(pubsub.ChannelStream, 64)
	defer ua()
	cb, ub := b.Subscribe(pubsub.ChannelStream, 64)
	defer ub()

	// Keep notifying until both listeners (separate connections, like two replicas) got one.
	gotA, gotB := false, false
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !gotA || !gotB {
		select {
		case m := <-ca:
			require.Equal(t, "hello", m)
			gotA = true
		case m := <-cb:
			require.Equal(t, "hello", m)
			gotB = true
		case <-tick.C:
			require.NoError(t, pubsub.Notify(ctx, d.Pool, pubsub.ChannelStream, "hello"))
		case <-deadline:
			t.Fatalf("fan-out incomplete: a=%v b=%v", gotA, gotB)
		}
	}
}
