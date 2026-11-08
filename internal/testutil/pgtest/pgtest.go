// Package pgtest creates an isolated, migrated PostgreSQL database per test.
// It uses INFRA_MONITOR_TEST_DATABASE_URL (admin URL), defaulting to the docker-compose
// Postgres on localhost:55432. Tests are skipped when Postgres is unreachable, unless
// CI or INFRA_MONITOR_REQUIRE_DB is set.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db"
)

const defaultURL = "postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable"

// AdminURL returns the admin connection URL used to create test databases.
func AdminURL() string {
	if u := os.Getenv("INFRA_MONITOR_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return defaultURL
}

// New returns a connected DB with all migrations applied.
func New(t testing.TB) *db.DB {
	t.Helper()
	dsn := NewURL(t)
	ctx := context.Background()
	d, err := db.Open(ctx, config.DatabaseConfig{DSN: dsn, MaxConns: 10}, Logger())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(ctx, d.Pool, Logger()); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

// NewURL creates an empty database and returns its URL (dropped at cleanup).
func NewURL(t testing.TB) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, AdminURL())
	if err != nil {
		if os.Getenv("CI") != "" || os.Getenv("INFRA_MONITOR_REQUIRE_DB") != "" {
			t.Fatalf("postgres unavailable: %v", err)
		}
		t.Skipf("postgres unavailable (run `make db-up`): %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "imtest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test db: %v", err)
	}
	_ = admin.Close(ctx)
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), AdminURL())
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	u, _ := url.Parse(AdminURL())
	u.Path = "/" + name
	return u.String()
}

// Logger returns a logger that discards output unless INFRA_MONITOR_TEST_LOG is set.
func Logger() *slog.Logger {
	if os.Getenv("INFRA_MONITOR_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
