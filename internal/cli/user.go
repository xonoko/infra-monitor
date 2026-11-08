package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/domain"
)

func readPassword(in io.Reader) (string, error) {
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	pw := strings.TrimRight(line, "\r\n")
	if pw == "" {
		return "", errors.New("empty password on stdin")
	}
	return pw, nil
}

func newUser(o *rootOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage local users"}

	var email, name, role string
	var stdin bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a user (password from stdin with --password-stdin)",
		Example: `  echo -n 's3cret-pass' | infra-monitor user create --email lucas@example.com --role admin --password-stdin
  infra-monitor user create --email sso-user@example.com --role operator   # SSO-only user`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := domain.ParseRole(role)
			if err != nil {
				return err
			}
			pw := ""
			if stdin {
				if pw, err = readPassword(cmd.InOrStdin()); err != nil {
					return err
				}
			}
			cfg, log, d, err := o.openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer d.Close()
			u, err := auth.NewService(cfg, d, log).CreateUser(cmd.Context(), email, name, r, pw)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created user %d %s (%s)\n", u.ID, u.Email, u.Role)
			return nil
		},
	}
	create.Flags().StringVar(&email, "email", "", "email (login)")
	create.Flags().StringVar(&name, "name", "", "display name")
	create.Flags().StringVar(&role, "role", "viewer", "viewer | operator | admin")
	create.Flags().BoolVar(&stdin, "password-stdin", false, "read the password from stdin")
	_ = create.MarkFlagRequired("email")

	var resetEmail string
	reset := &cobra.Command{
		Use:   "reset-password",
		Short: "Set a user's password (from stdin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw, err := readPassword(cmd.InOrStdin())
			if err != nil {
				return err
			}
			cfg, log, d, err := o.openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer d.Close()
			if err := auth.NewService(cfg, d, log).SetPassword(cmd.Context(), resetEmail, pw); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "password updated")
			return nil
		},
	}
	reset.Flags().StringVar(&resetEmail, "email", "", "user email")
	reset.Flags().Bool("password-stdin", true, "read the password from stdin (always on)")
	_ = reset.MarkFlagRequired("email")

	list := &cobra.Command{
		Use:   "list",
		Short: "List users",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, log, d, err := o.openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer d.Close()
			us, err := auth.NewService(cfg, d, log).Users(cmd.Context())
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tEMAIL\tROLE\tDISABLED")
			for _, u := range us {
				fmt.Fprintf(w, "%d\t%s\t%s\t%v\n", u.ID, u.Email, u.Role, u.Disabled)
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(create, reset, list)
	return cmd
}

func newToken(o *rootOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Manage API tokens"}
	var name, role, expires string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create an API token (printed once)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := domain.ParseRole(role)
			if err != nil {
				return err
			}
			var ttl time.Duration
			if expires != "" {
				if ttl, err = time.ParseDuration(expires); err != nil {
					return fmt.Errorf("--expires-in: %w", err)
				}
			}
			cfg, log, d, err := o.openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer d.Close()
			raw, _, err := auth.NewService(cfg, d, log).CreateToken(cmd.Context(), name, r, nil, ttl)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), raw)
			fmt.Fprintln(os.Stderr, "store this token now: it is not shown again")
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "token name")
	create.Flags().StringVar(&role, "role", "viewer", "viewer | operator | admin")
	create.Flags().StringVar(&expires, "expires-in", "", "e.g. 720h (default: never)")
	_ = create.MarkFlagRequired("name")
	cmd.AddCommand(create)
	return cmd
}
