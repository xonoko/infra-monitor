// Package cli implements the infra-monitor command line (cobra).
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/logger"
)

type rootOpts struct {
	configPath string
}

func newRoot() *cobra.Command {
	o := &rootOpts{}
	root := &cobra.Command{
		Use:           "infra-monitor",
		Short:         "Alert and incident hub with AI investigation, actions and chat channels",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&o.configPath, "config", "c", os.Getenv("INFRA_MONITOR_CONFIG"), "config file (env INFRA_MONITOR_CONFIG)")
	root.AddCommand(newServe(o), newMigrate(o), newUser(o), newToken(o), newVersion(), newOpenAPI(o))
	return root
}

// Execute runs the CLI.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := newRoot().ExecuteContext(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	return err
}

func (o *rootOpts) load() (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load(o.configPath)
	if err != nil {
		return nil, nil, err
	}
	format := cfg.LogFormat
	if cfg.IsDev() && format == "" {
		format = "text"
	}
	return cfg, logger.New(cfg.LogLevel, format), nil
}

// openDB loads config and connects, for CLI commands that only need the database.
func (o *rootOpts) openDB(ctx context.Context) (*config.Config, *slog.Logger, *db.DB, error) {
	cfg, log, err := o.load()
	if err != nil {
		return nil, nil, nil, err
	}
	d, err := db.Open(ctx, cfg.Database, log)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, log, d, nil
}
