package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/xonoko/infra-monitor/internal/db"
)

func newMigrate(o *rootOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "migrate", Short: "Database migrations"}
	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Apply pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, log, d, err := o.openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer d.Close()
			return db.Migrate(cmd.Context(), d.Pool, log)
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "Show migration status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, d, err := o.openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer d.Close()
			st, err := db.Status(cmd.Context(), d.Pool)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "VERSION\tNAME\tAPPLIED")
			for _, s := range st {
				applied := "pending"
				if s.AppliedAt != nil {
					applied = s.AppliedAt.Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", s.Version, s.Name, applied)
			}
			return w.Flush()
		},
	})
	return cmd
}
