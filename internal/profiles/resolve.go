package profiles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/behnambm/gcli/internal/config"
)

// ErrNotFound marks "no such profile" errors (unknown name, or a requested
// profile whose file is missing). The CLI maps it to exit code 1.
var ErrNotFound = errors.New("profile not found")

type ResolveOptions struct {
	FlagProfile string
	FlagURL     string
	FlagToken   string
	Path        string // profiles.yaml path; "" = profiles.Path()
	Timeout     time.Duration
	Output      string
	NoColor     bool
	Verbose     bool
}

// Resolve picks the active profile: --profile > GCLI_PROFILE > default: >
// legacy GRAFANA_URL/GRAFANA_TOKEN env. --url/--token flags override any
// profile value. Returns the zero Profile on the legacy path.
func Resolve(o ResolveOptions) (config.Config, Profile, error) {
	path := o.Path
	if path == "" {
		p, err := Path()
		if err != nil {
			return config.Config{}, Profile{}, err
		}
		path = p
	}
	name := o.FlagProfile
	if name == "" {
		name = os.Getenv("GCLI_PROFILE")
	}
	if name == "" {
		// Only consult the default: marker when the file exists — legacy
		// users have no file and must not get a "no profiles file" error.
		// A file that exists but fails to parse is a config error: surface
		// it instead of silently falling back to legacy env.
		if f, err := Load(path); err == nil {
			name = f.Default
		} else if !errors.Is(err, fs.ErrNotExist) {
			return config.Config{}, Profile{}, err
		}
	}
	if name == "" {
		cfg, err := config.Load(o.FlagURL, o.FlagToken, o.Timeout, o.Output, o.NoColor, o.Verbose)
		return cfg, Profile{}, err
	}
	f, err := Load(path)
	if err != nil {
		return config.Config{}, Profile{}, fmt.Errorf("%w: profile %q requested but %w — run `gcli profiles add %s`", ErrNotFound, name, err, name)
	}
	p, ok := f.Profiles[name]
	if !ok {
		return config.Config{}, Profile{}, fmt.Errorf("%w: profile %q not found in %s — known profiles: %s", ErrNotFound, name, path, strings.Join(knownNames(f), ", "))
	}
	url := firstNonEmpty(o.FlagURL, p.URL)
	if url == "" {
		return config.Config{}, Profile{}, fmt.Errorf("profile %q has no url", name)
	}
	out := o.Output
	if out == "" {
		out = "table"
	}
	switch out {
	case "table", "json", "csv":
	default:
		return config.Config{}, Profile{}, fmt.Errorf("invalid --output %q: must be table, json or csv", out)
	}
	cfg := config.Config{
		URL:               strings.TrimRight(url, "/"),
		User:              p.User,
		Pass:              p.Pass,
		Token:             p.Token,
		OrgID:             p.OrgID,
		DefaultDatasource: p.DefaultDatasource,
		Timeout:           o.Timeout,
		Output:            out,
		NoColor:           o.NoColor,
		Verbose:           o.Verbose,
	}
	if o.FlagToken != "" {
		cfg.Token = o.FlagToken
		cfg.User, cfg.Pass = "", ""
	}
	return cfg, p, nil
}

func knownNames(f File) []string {
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
