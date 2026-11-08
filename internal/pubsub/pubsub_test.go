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
	ca, ua := a.Subscribe(pubsub.ChannelStream, 4)
	defer ua()
	cb, ub := b.Subscribe(pubsub.ChannelStream, 4)
	defer ub()

	require.Eventually(t, func() bool {
		_ = pubsub.Notify(ctx, d.Pool, pubsub.ChannelStream, "hello")
		select {
		case m := <-ca:
			require.Equal(t, "hello", m)
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)
	select {
	case m := <-cb:
		require.Equal(t, "hello", m)
	case <-time.After(2 * time.Second):
		t.Fatal("second replica did not receive the notification")
	}
}
