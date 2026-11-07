package ctl

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/platform/private"
)

// configFlag names the profiles file. It is not a setting, so it is never
// bound.
const configFlag = "config"

// Profile is a named endpoint and its credential: an API key's ID and
// secret, or a token.
type Profile struct {
	Endpoint string `mapstructure:"endpoint" yaml:"endpoint"`
	Key      string `mapstructure:"key"      yaml:"key,omitempty"`
	Secret   string `mapstructure:"secret"   yaml:"secret,omitempty"`
	Token    string `mapstructure:"token"    yaml:"token,omitempty"`
}

// File is the profiles file: the profiles by name and the current one.
type File struct {
	Current  string             `mapstructure:"current"  yaml:"current,omitempty"`
	Profiles map[string]Profile `mapstructure:"profiles" yaml:"profiles,omitempty"`
}

// profileName is the form of a profile's name. viper lowercases keys and
// splits them at dots, so a name holds neither upper case nor a dot.
var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// configPath is --config, else config.yaml in the user configuration
// directory's infraweavectl.
func configPath(fs *pflag.FlagSet) (string, error) {
	if p, _ := fs.GetString(configFlag); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate the profiles file: %w", err)
	}
	return filepath.Join(dir, "infraweavectl", "config.yaml"), nil
}

// readFile decodes the profiles file strictly, rejecting a key it does not
// know. A file that does not exist holds no profiles.
func readFile(path string) (File, error) {
	var f File
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return f, fmt.Errorf("read %s: %w", path, err)
	}
	if err := v.UnmarshalExact(&f); err != nil {
		return f, fmt.Errorf("decode %s: %w", path, err)
	}
	return f, nil
}

// writeFile replaces the profiles file whole, readable by its owner only
// (private.Restrict), since it holds secrets. It writes a temporary file and renames it, so a
// failed write leaves the old file.
func writeFile(path string, f File) error {
	b, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode profiles: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	// Restricted before it holds a secret; the rename keeps the restriction.
	if err := private.Restrict(tmp.Name()); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// edit reads the profiles file, applies fn and writes the result.
func edit(cmd *cobra.Command, fn func(*File) error) error {
	path, err := configPath(cmd.Flags())
	if err != nil {
		return err
	}
	f, err := readFile(path)
	if err != nil {
		return err
	}
	if err := fn(&f); err != nil {
		return err
	}
	return writeFile(path, f)
}

// held fails for a profile f does not hold.
func held(f *File, name string) error {
	if _, ok := f.Profiles[name]; !ok {
		return fmt.Errorf("%w %q", ErrUnknownProfile, name)
	}
	return nil
}

func profileCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Manage named endpoints and their credentials"}
	set := &cobra.Command{
		Use:   "set NAME",
		Short: "Create or replace a profile from --endpoint and a credential",
		Long: "Create or replace a profile from --endpoint and, with --api-key, the API key " +
			"whose secret is standard input's first line, or, with --token, the token " +
			"standard input holds. The first profile becomes the current one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !profileName.MatchString(name) {
				return fmt.Errorf("%w: profile name %q is not 1 to 63 lower-case letters, digits, "+
					"hyphens and underscores", ErrInput, name)
			}
			p := Profile{}
			p.Endpoint, _ = cmd.Flags().GetString("endpoint")
			p.Key, _ = cmd.Flags().GetString("api-key")
			token, _ := cmd.Flags().GetBool("token")
			if err := checkEndpoint(p.Endpoint); err != nil {
				return err
			}
			var err error
			switch {
			case p.Key != "" && token:
				return fmt.Errorf("%w: --api-key and --token both name the credential", ErrInput)
			case p.Key != "":
				p.Secret, err = stdin(cmd, "API key secret")
			case token:
				p.Token, err = stdin(cmd, "token")
			}
			if err != nil {
				return err
			}
			return edit(cmd, func(f *File) error {
				if f.Profiles == nil {
					f.Profiles = map[string]Profile{}
				}
				f.Profiles[name] = p
				if f.Current == "" {
					f.Current = name
				}
				return nil
			})
		},
	}
	set.Flags().Bool("token", false, "read a token from standard input as the credential")
	cmd.AddCommand(set,
		&cobra.Command{
			Use:   "list",
			Short: "List the profiles, the current one marked *",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				path, err := configPath(cmd.Flags())
				if err != nil {
					return err
				}
				f, err := readFile(path)
				if err != nil {
					return err
				}
				for _, name := range slices.Sorted(maps.Keys(f.Profiles)) {
					mark := " "
					if name == f.Current {
						mark = "*"
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\n", mark, name,
						f.Profiles[name].Endpoint); err != nil {
						return fmt.Errorf("write: %w", err)
					}
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "use NAME",
			Short: "Make a profile the current one",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return edit(cmd, func(f *File) error {
					f.Current = args[0]
					return held(f, args[0])
				})
			},
		},
		&cobra.Command{
			Use:   "delete NAME",
			Short: "Delete a profile and its credential",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return edit(cmd, func(f *File) error {
					if err := held(f, args[0]); err != nil {
						return err
					}
					delete(f.Profiles, args[0])
					if f.Current == args[0] {
						f.Current = ""
					}
					return nil
				})
			},
		},
	)
	return cmd
}
