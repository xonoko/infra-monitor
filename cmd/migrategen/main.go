// Command migrategen writes a new versioned migration by diffing the ent schemas
// against the replayed migration directory on a scratch dev database.
//
//	go run ./cmd/migrategen -name add_foo -dev-url postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	stdsql "database/sql"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/xonoko/infra-monitor/internal/db/ent/migrate"
)

func main() {
	stdsql.Register("postgres", stdlib.GetDefaultDriver())
	name := flag.String("name", "", "migration name")
	devURL := flag.String("dev-url", os.Getenv("INFRA_MONITOR_DEV_DATABASE_URL"), "admin URL of a Postgres server used as scratch dev database")
	dirPath := flag.String("dir", "internal/db/migrations", "migration directory")
	flag.Parse()
	if *name == "" || *devURL == "" {
		log.Fatal("-name and -dev-url are required")
	}
	ctx := context.Background()
	scratch, err := scratchDB(ctx, *devURL)
	if err != nil {
		log.Fatal(err)
	}
	dir, err := atlasmigrate.NewLocalDir(*dirPath)
	if err != nil {
		log.Fatal(err)
	}
	err = migrate.NamedDiff(ctx, scratch, *name,
		schema.WithDir(dir),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(dialect.Postgres),
		schema.WithFormatter(atlasmigrate.DefaultFormatter),
		schema.WithDropColumn(true),
		schema.WithDropIndex(true),
	)
	if err != nil {
		log.Fatalf("diff: %v", err)
	}
	fmt.Println("migration written to", *dirPath)
}

// scratchDB recreates an empty database "infra_monitor_atlas_dev" and returns its URL.
func scratchDB(ctx context.Context, adminURL string) (string, error) {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	const dbName = "infra_monitor_atlas_dev"
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName); err != nil {
		return "", err
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		return "", err
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	q := u.Query()
	q.Set("search_path", "public")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
