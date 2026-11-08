package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

func TestMigrateIsIdempotentAndConcurrent(t *testing.T) {
	d := pgtest.New(t) // already migrated once
	ctx := context.Background()
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() { errs <- db.Migrate(ctx, d.Pool, pgtest.Logger()) }()
	}
	for i := 0; i < 3; i++ {
		require.NoError(t, <-errs)
	}
	st, err := db.Status(ctx, d.Pool)
	require.NoError(t, err)
	require.NotEmpty(t, st)
	for _, s := range st {
		require.NotNil(t, s.AppliedAt, s.Version)
	}
	// ent can talk to the migrated schema
	n, err := d.Ent.Incident.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}
