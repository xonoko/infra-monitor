// Package db opens the PostgreSQL pool shared by ent, the job queue, leases and LISTEN/NOTIFY.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db/ent"
)

type DB struct {
	Pool *pgxpool.Pool
	SQL  *sql.DB
	Ent  *ent.Client
}

// Open connects to Postgres. Every query gets a span through otelpgx.
func Open(ctx context.Context, cfg config.DatabaseConfig, log *slog.Logger) (*DB, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL())
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	pcfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pcfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database %s:%d/%s: %w", pcfg.ConnConfig.Host, pcfg.ConnConfig.Port, pcfg.ConnConfig.Database, err)
	}
	sqldb := stdlib.OpenDBFromPool(pool)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(entdialect.Postgres, sqldb)))
	log.Info("database connected", "host", pcfg.ConnConfig.Host, "database", pcfg.ConnConfig.Database)
	return &DB{Pool: pool, SQL: sqldb, Ent: client}, nil
}

func (d *DB) Close() {
	_ = d.Ent.Close()
	d.Pool.Close()
}

// Ping checks connectivity (used by /readyz).
func (d *DB) Ping(ctx context.Context) error { return d.Pool.Ping(ctx) }

// WithTx runs fn in an ent transaction, rolling back on error or panic.
func WithTx(ctx context.Context, client *ent.Client, fn func(tx *ent.Tx) error) (err error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err = fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil {
			return fmt.Errorf("%w (rollback: %v)", err, rerr)
		}
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
