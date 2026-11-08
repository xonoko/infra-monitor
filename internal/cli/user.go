package cli

import "github.com/spf13/cobra"

func newUser(o *rootOpts) *cobra.Command  { return &cobra.Command{Use: "user", Short: "Manage local users"} }
func newToken(o *rootOpts) *cobra.Command { return &cobra.Command{Use: "token", Short: "Manage API tokens"} }
