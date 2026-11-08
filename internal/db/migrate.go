package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xonoko/infra-monitor/internal/db/migrations"
)

// migrationLockKey is the advisory lock that serializes migrations across replicas.
const migrationLockKey int64 = 0x696d5f6d6967 // "im_mig"

type MigrationStatus struct {
	Version   string
	Name      string
	AppliedAt *time.Time
}

type migrationFile struct {
	version, name, sql, sum string
}

func loadMigrations() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return nil, err
		}
		base := strings.TrimSuffix(e.Name(), ".sql")
		version, name, _ := strings.Cut(base, "_")
		h := sha256.Sum256(b)
		out = append(out, migrationFile{version: version, name: name, sql: string(b), sum: hex.EncodeToString(h[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS infra_monitor_migrations (
	version text PRIMARY KEY,
	name text NOT NULL,
	checksum text NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// Migrate applies pending embedded migrations. Replicas starting together wait on an advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	files, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()
	if _, err := conn.Exec(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	applied := map[string]string{}
	rows, err := conn.Query(ctx, "SELECT version, checksum FROM infra_monitor_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v, s string
		if err := rows.Scan(&v, &s); err != nil {
			rows.Close()
			return err
		}
		applied[v] = s
	}
	rows.Close()
	n := 0
	for _, f := range files {
		if sum, ok := applied[f.version]; ok {
			if sum != f.sum {
				log.Warn("applied migration changed on disk", "version", f.version, "name", f.name)
			}
			continue
		}
		start := time.Now()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, f.sql); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s_%s: %w", f.version, f.name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO infra_monitor_migrations (version, name, checksum) VALUES ($1, $2, $3)", f.version, f.name, f.sum); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		n++
		log.Info("migration applied", "version", f.version, "name", f.name, "duration", time.Since(start))
	}
	log.Info("migrations up to date", "applied", n, "total", len(files))
	return nil
}

// Status lists embedded migrations and when they were applied.
func Status(ctx context.Context, pool *pgxpool.Pool) ([]MigrationStatus, error) {
	files, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, createMigrationsTable); err != nil {
		return nil, err
	}
	applied := map[string]time.Time{}
	rows, err := pool.Query(ctx, "SELECT version, applied_at FROM infra_monitor_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var at time.Time
		if err := rows.Scan(&v, &at); err != nil {
			return nil, err
		}
		applied[v] = at
	}
	out := make([]MigrationStatus, 0, len(files))
	for _, f := range files {
		st := MigrationStatus{Version: f.version, Name: f.name}
		if at, ok := applied[f.version]; ok {
			st.AppliedAt = &at
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
