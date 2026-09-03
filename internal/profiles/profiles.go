// Package profiles manages ~/.config/gcli/profiles.yaml: named Grafana
// connection profiles (url + token or basic auth, optional org + default
// datasource) plus the active `default:` marker.
package profiles

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Profile struct {
	Name              string `yaml:"-"`
	URL               string `yaml:"url"`
	Token             string `yaml:"token,omitempty"`
	User              string `yaml:"user,omitempty"`
	Pass              string `yaml:"pass,omitempty"`
	OrgID             string `yaml:"orgId,omitempty"`
	DefaultDatasource string `yaml:"defaultDatasource,omitempty"`
}

type File struct {
	Default  string             `yaml:"default,omitempty"`
	Profiles map[string]Profile `yaml:"profiles"`
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config dir: %w", err)
	}
	return filepath.Join(dir, "gcli", "profiles.yaml"), nil
}

func Load(path string) (File, error) {
	var f File
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read profiles file %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	for name, p := range f.Profiles {
		p.Name = name
		if p.URL == "" {
			return File{}, fmt.Errorf("profile %q: url is required", name)
		}
		if p.Token != "" && (p.User != "" || p.Pass != "") {
			return File{}, fmt.Errorf("profile %q: set either token or user/pass, not both", name)
		}
		if p.User != "" && p.Pass == "" {
			return File{}, fmt.Errorf("profile %q: user requires pass", name)
		}
		if p.Pass != "" && p.User == "" {
			return File{}, fmt.Errorf("profile %q: pass requires user", name)
		}
		f.Profiles[name] = p
	}
	return f, nil
}

func Save(path string, f File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode profiles: %w", err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Chmod(path, 0600)
}

// WarnIfWorldReadable returns a chmod hint when an existing profiles file is
// readable by group/other, "" otherwise (missing file included).
func WarnIfWorldReadable(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if st.Mode().Perm()&0077 != 0 {
		return fmt.Sprintf("warning: %s is world-readable — run: chmod 600 %s", path, path)
	}
	return ""
}
