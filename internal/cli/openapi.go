package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/xonoko/infra-monitor/internal/api"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/logger"
)

func newOpenAPI(o *rootOpts) *cobra.Command {
	return &cobra.Command{
		Use:    "openapi",
		Short:  "Print the OpenAPI spec (used to generate the UI client)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(o.configPath)
			if err != nil {
				return err
			}
			srv := api.New(cfg, logger.New("error", "text"), api.FullDepsForSpec())
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(srv.API().OpenAPI())
		},
	}
}
