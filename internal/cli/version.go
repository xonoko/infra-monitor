package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/xonoko/infra-monitor/internal/buildinfo"
)

func newVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "infra-monitor %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		},
	}
}
