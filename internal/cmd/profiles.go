package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/profiles"
)

var (
	flagProfURL        string
	flagProfToken      string
	flagProfUser       string
	flagProfPass       string
	flagProfOrgID      string
	flagProfDefaultDS  string
	flagProfSetDefault bool
	flagProfForce      bool
)

func init() {
	rootCmd.AddCommand(profilesCmd)
}

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "Manage Grafana connection profiles (profiles.yaml)",
}

var profilesAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add or update a profile (interactive, or --url/--token/--user/--pass)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if name == "list" || name == "add" || name == "use" || name == "remove" || name == "test" {
			return fmt.Errorf("%q is a reserved word — pick another profile name", name)
		}
		path, err := profiles.Path()
		if err != nil {
			return err
		}
		f := profiles.File{Profiles: map[string]profiles.Profile{}}
		existing, err := profiles.Load(path)
		switch {
		case err == nil:
			f = existing
		case errors.Is(err, fs.ErrNotExist):
			// fresh start
		default:
			return fmt.Errorf("existing profiles file is invalid — refusing to overwrite: %w", err)
		}
		p := f.Profiles[name]
		p.Name = name
		haveFlags := flagProfURL != "" || flagProfToken != "" || flagProfUser != "" || flagProfPass != ""
		if !haveFlags {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("not a TTY and no flags — use: gcli profiles add %s --url <u> (--token <t> | --user <u> --pass <p>)", name)
			}
			fmt.Fprint(cmd.OutOrStdout(), "Grafana URL: ")
			url, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if err != nil {
				return fmt.Errorf("read url: %w", err)
			}
			flagProfURL = strings.TrimSpace(url)
			fmt.Fprint(cmd.OutOrStdout(), "Auth method (token|basic): ")
			method, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			switch strings.TrimSpace(method) {
			case "basic":
				fmt.Fprint(cmd.OutOrStdout(), "User: ")
				u, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				flagProfUser = strings.TrimSpace(u)
				fmt.Fprint(cmd.OutOrStdout(), "Password (hidden): ")
				pass, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.OutOrStdout())
				if err != nil {
					return fmt.Errorf("read password: %w", err)
				}
				flagProfPass = string(pass)
			default:
				fmt.Fprint(cmd.OutOrStdout(), "Token (hidden): ")
				tok, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.OutOrStdout())
				if err != nil {
					return fmt.Errorf("read token: %w", err)
				}
				flagProfToken = string(tok)
			}
		}
		p.URL = strings.TrimRight(flagProfURL, "/")
		p.Token = flagProfToken
		p.User = flagProfUser
		p.Pass = flagProfPass
		p.OrgID = flagProfOrgID
		p.DefaultDatasource = flagProfDefaultDS
		if p.URL == "" {
			return fmt.Errorf("url is required — pass --url <u>")
		}
		// Mirror Load's auth checks so a bad profile can never be saved
		// (it would fail the next Load or send unauthenticated requests).
		if p.Token != "" && (p.User != "" || p.Pass != "") {
			return fmt.Errorf("set either token or user/pass, not both")
		}
		if p.User != "" && p.Pass == "" {
			return fmt.Errorf("user requires pass — pass --pass <password>")
		}
		if p.Pass != "" && p.User == "" {
			return fmt.Errorf("pass requires user — pass --user <name>")
		}
		if p.Token == "" && p.User == "" {
			return fmt.Errorf("no auth given — pass --token or --user/--pass")
		}
		f.Profiles[name] = p
		if flagProfSetDefault {
			f.Default = name
		}
		if err := profiles.Save(path, f); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "profile %q saved to %s\n", name, path)
		if w := profiles.WarnIfWorldReadable(path); w != "" {
			fmt.Fprintln(cmd.OutOrStdout(), w)
		}
		return nil
	},
}

var profilesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List profiles (secrets never shown)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, f, err := loadProfilesFile()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tURL\tAUTH\tORG\tDEFAULT")
		for _, name := range sortedNames(f) {
			p := f.Profiles[name]
			auth := "token"
			if p.User != "" {
				auth = "basic"
			}
			mark := ""
			if name == f.Default {
				mark = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, p.URL, auth, p.OrgID, mark)
		}
		return w.Flush()
	},
}

var profilesUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the default profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, f, err := loadProfilesFile()
		if err != nil {
			return err
		}
		if _, ok := f.Profiles[args[0]]; !ok {
			return fmt.Errorf("profile %q not found — known profiles: %s", args[0], strings.Join(sortedNames(f), ", "))
		}
		f.Default = args[0]
		if err := profiles.Save(path, f); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "default profile set to %q\n", args[0])
		return nil
	},
}

var profilesRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Delete a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, f, err := loadProfilesFile()
		if err != nil {
			return err
		}
		if _, ok := f.Profiles[args[0]]; !ok {
			return fmt.Errorf("profile %q not found — known profiles: %s", args[0], strings.Join(sortedNames(f), ", "))
		}
		if f.Default == args[0] && !flagProfForce {
			return fmt.Errorf("%q is the default profile — pass --force to remove it anyway", args[0])
		}
		delete(f.Profiles, args[0])
		if f.Default == args[0] {
			f.Default = ""
		}
		if err := profiles.Save(path, f); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "profile %q removed\n", args[0])
		return nil
	},
}

var profilesTestCmd = &cobra.Command{
	Use:   "test [name]",
	Short: "Smoke-test a profile: /api/health + version",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			flagProfile = args[0]
		}
		cfg, _, err := profiles.Resolve(profiles.ResolveOptions{
			FlagProfile: flagProfile,
			FlagURL:     flagURL,
			FlagToken:   flagToken,
			Timeout:     flagTimeout,
			Output:      flagOutput,
			NoColor:     flagNoColor,
			Verbose:     flagVerbose,
		})
		if err != nil {
			return err
		}
		c := api.NewClient(cfg)
		if cfg.Verbose {
			c.LogW = cmd.ErrOrStderr()
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
		defer cancel()
		version, err := c.Version(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "OK: grafana %s at %s\n", version["version"], cfg.URL)
		return nil
	},
}

func loadProfilesFile() (string, profiles.File, error) {
	path, err := profiles.Path()
	if err != nil {
		return "", profiles.File{}, err
	}
	f, err := profiles.Load(path)
	if err != nil {
		return "", profiles.File{}, fmt.Errorf("%v — run `gcli profiles add <name>` to create one", err)
	}
	return path, f, nil
}

func sortedNames(f profiles.File) []string {
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func init() {
	profilesAddCmd.Flags().StringVar(&flagProfURL, "url", "", "Grafana URL")
	profilesAddCmd.Flags().StringVar(&flagProfToken, "token", "", "service-account token")
	profilesAddCmd.Flags().StringVar(&flagProfUser, "user", "", "basic-auth user")
	profilesAddCmd.Flags().StringVar(&flagProfPass, "pass", "", "basic-auth password")
	profilesAddCmd.Flags().StringVar(&flagProfOrgID, "org-id", "", "optional org id (X-Grafana-Org-Id header)")
	profilesAddCmd.Flags().StringVar(&flagProfDefaultDS, "default-datasource", "", "optional default datasource name/uid")
	profilesAddCmd.Flags().BoolVar(&flagProfSetDefault, "set-default", false, "make this the default profile")
	profilesRemoveCmd.Flags().BoolVar(&flagProfForce, "force", false, "remove even if it is the default profile")
	profilesCmd.AddCommand(profilesAddCmd, profilesListCmd, profilesUseCmd, profilesRemoveCmd, profilesTestCmd)
}
