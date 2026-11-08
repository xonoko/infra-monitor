package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/xonoko/infra-monitor/internal/app"
)

func newServe(o *rootOpts) *cobra.Command {
	var noMigrate bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the API, UI and background workers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, log, err := o.load()
			if err != nil {
				return err
			}
			a, err := app.Build(cmd.Context(), cfg, log, app.Options{SkipMigrate: noMigrate})
			if err != nil {
				return err
			}
			defer a.Close(context.Background())
			err = a.Run(cmd.Context())
			log.Info("stopped")
			return err
		},
	}
	cmd.Flags().BoolVar(&noMigrate, "no-migrate", false, "do not apply migrations at startup")
	return cmd
}
