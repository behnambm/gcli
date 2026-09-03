package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/config"
	"github.com/behnambm/gcli/internal/frames"
	"github.com/behnambm/gcli/internal/profiles"
	"github.com/behnambm/gcli/internal/render"
)

type result struct {
	res frames.Result
	raw []byte // printed verbatim instead of rendered
}

var lastCfg config.Config

// dsArg resolves the datasource positional: explicit arg wins, else the
// profile's defaultDatasource, else an actionable error. It reads lastCfg,
// which run() refreshes before invoking a command's callback — so call it
// inside the callback, not at RunE top, or the default would come from a
// previous command's config.
func dsArg(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if lastCfg.DefaultDatasource == "" {
		return "", fmt.Errorf("no datasource given — pass <datasource-uid-or-name>, or set defaultDatasource in your profile (`gcli profiles add <name> --default-datasource <ds>`)")
	}
	return lastCfg.DefaultDatasource, nil
}

func run(cmd *cobra.Command, fn func(ctx context.Context, c *api.Client) (result, error)) error {
	client, cfg, err := clientFromFlags(cmd)
	if err != nil {
		return err
	}
	lastCfg = cfg
	ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
	defer cancel()
	r, err := fn(ctx, client)
	if r.raw != nil {
		fmt.Fprintln(cmd.OutOrStdout(), string(r.raw))
		return err
	}
	if shouldRender(err, r) {
		if rerr := render.Render(cmd.OutOrStdout(), r.res, outputOptions(cfg)); rerr != nil {
			if err != nil {
				return err
			}
			return rerr
		}
	}
	return err
}

// shouldRender reports whether run() should render r.res: always on success,
// and on error only when frames exist so partial successes are still shown
// without letting a render error (e.g. CSV "frame out of range") mask the
// underlying query error.
func shouldRender(err error, r result) bool {
	return err == nil || len(r.res.Frames) > 0
}

func clientFromFlags(cmd *cobra.Command) (*api.Client, config.Config, error) {
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
		return nil, config.Config{}, err
	}
	c := api.NewClient(cfg)
	if cfg.Verbose {
		c.LogW = cmd.ErrOrStderr()
	}
	return c, cfg, nil
}

func outputOptions(cfg config.Config) render.Options {
	return render.Options{
		Output:   cfg.Output,
		NoColor:  cfg.NoColor,
		Color:    !cfg.NoColor && isTTY(os.Stdout),
		FrameIdx: flagFrame,
	}
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func exitCode(err error) int {
	var he *api.HTTPError
	if errors.As(err, &he) {
		return he.ExitCode()
	}
	var qe *api.QueryError
	if errors.As(err, &qe) {
		return 5
	}
	if errors.Is(err, config.ErrMissingURL) || errors.Is(err, config.ErrMissingToken) || errors.Is(err, profiles.ErrNotFound) {
		return 1
	}
	return 2
}

func hintOf(err error) string {
	var he *api.HTTPError
	if errors.As(err, &he) {
		return he.Hint()
	}
	var qe *api.QueryError
	if errors.As(err, &qe) {
		return "query failed — check the query syntax for this datasource type"
	}
	if exitCode(err) == 1 {
		return "set up a profile: `gcli profiles add <name>` — or export GRAFANA_URL/GRAFANA_TOKEN; run `gcli help` for the full guide"
	}
	return ""
}
