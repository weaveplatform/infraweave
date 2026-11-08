// Command specref maintains the Spacelift API references, one per product
// (--product: deploy, the default, in api/reference/spacelift, or flows in
// api/reference/spacelift-flows): fetch refreshes the pinned upstream
// snapshots, generate derives the OpenAPI spec and coverage report from
// them, check fails when the committed output is stale, scaffold copies a
// reference tag into a served-API fragment, inventory writes or checks the
// product's inventory register (and, for Deploy, the data model inventory
// report, which summarizes it), and parity decides each reference operation's outcome
// (served, served with divergence, superseded or missing), checks the
// capabilities register's states against the acceptance results with
// --states, and rewrites the parity baseline; actions writes or checks
// Deploy's action catalogue, internal/access/api/actions.gen.go. Logic lives in
// internal/devtools/specref.
//
// ref: report 29
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/weaveplatform/infraweave/internal/devtools/specref"
	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
)

// Pins used when no sources.lock.yaml exists yet. Once one does, its values
// are the defaults, so a local fetch never silently moves a pin that a
// refresh PR set.
const (
	defaultEndpoint        = "https://demo.app.spacelift.io/graphql"
	defaultBrunoRepo       = "spacelift-io/spacelift-api-bruno"
	defaultBrunoRef        = "7cad4574954b569df083b2a809b68371fc385e6d"
	defaultProviderRepo    = "spacelift-io/terraform-provider-spacelift"
	defaultProviderVersion = "1.57.0"

	defaultFlowsProviderRepo = "spacelift-io/terraform-provider-flows"
	defaultFlowsProviderRef  = "861f72a3bac4c8b6de4caacc55cb8f37513fceae"
	defaultFlowctlRepo       = "spacelift-io/flowctl"
	defaultFlowctlRef        = "96bdd03303a60fca5c208c8a167faa80af40021d"
	defaultSDKPackage        = "@slflows/sdk"
	defaultSDKVersion        = "0.14.0"
	defaultTemplates         = "https://docs.spacelift.io/flows/templates.json"
	defaultRegistry          = "https://registry.useflows.com/core"
)

// config is every setting, from flag > SPECREF_* environment > --config file.
type config struct {
	Product         string `mapstructure:"product"`
	Dir             string `mapstructure:"dir"`
	Endpoint        string `mapstructure:"endpoint"`
	Codeload        string `mapstructure:"codeload"`
	BrunoRepo       string `mapstructure:"bruno-repo"`
	BrunoRef        string `mapstructure:"bruno-ref"`
	ProviderRepo    string `mapstructure:"provider-repo"`
	ProviderVersion string `mapstructure:"provider-version"`
	Terraform       string `mapstructure:"terraform"`
	// Flows fetch settings.
	FlowsProviderRepo string `mapstructure:"flows-provider-repo"`
	FlowsProviderRef  string `mapstructure:"flows-provider-ref"`
	FlowctlRepo       string `mapstructure:"flowctl-repo"`
	FlowctlRef        string `mapstructure:"flowctl-ref"`
	SDKPackage        string `mapstructure:"sdk-package"`
	SDKVersion        string `mapstructure:"sdk-version"`
	NPM               string `mapstructure:"npm"`
	Templates         string `mapstructure:"templates"`
	Registry          string `mapstructure:"registry"`
	Timeout           string `mapstructure:"timeout"`
	Tag               string `mapstructure:"tag"`
	Out               string `mapstructure:"out"`
	Catalogue         string `mapstructure:"catalogue"`
	Generated         string `mapstructure:"generated"`
	Register          string `mapstructure:"register"`
	Served            string `mapstructure:"served"`
	Baseline          string `mapstructure:"baseline"`
	Force             bool   `mapstructure:"force"`
	Check             bool   `mapstructure:"check"`
	WriteBaseline     bool   `mapstructure:"write-baseline"`
	Renames           string `mapstructure:"renames"`
	Supersessions     string `mapstructure:"supersessions"`
	States            bool   `mapstructure:"states"`
	Cucumber          string `mapstructure:"cucumber"`
	Capabilities      string `mapstructure:"capabilities"`
	List              bool   `mapstructure:"list"`
}

// errParity is returned by parity --check when the check fails; the report
// above it says why.
var errParity = errors.New("parity check failed")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := newRoot().ExecuteContext(ctx)
	stop()
	if err != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	v := viper.New()
	var cfgFile string
	root := &cobra.Command{
		Use:           "specref",
		Short:         "Maintain the Spacelift API reference (GraphQL → REST OpenAPI 3)",
		Version:       buildinfo.Version(),
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return loadConfig(v, cmd, cfgFile)
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&cfgFile, "config", "", "YAML config file (unknown keys are rejected)")
	pf.String("product", specref.Deploy.Name, "Spacelift product whose reference to work on")
	pf.String("dir", "", "reference directory (default the product's, such as "+
		specref.Deploy.Dir+")")

	fetch := &cobra.Command{
		Use:   "fetch",
		Short: "Refresh the upstream snapshots and sources.lock.yaml (network, terraform)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			timeout, err := time.ParseDuration(c.Timeout)
			if err != nil {
				return fmt.Errorf("timeout: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			fetcher := &specref.Fetcher{
				Client:    &http.Client{Timeout: 2 * time.Minute},
				UserAgent: "infraweave-specref/" + buildinfo.Version() + " (+https://github.com/weaveplatform/infraweave)",
				Retries:   3,
				Backoff:   2 * time.Second,
			}
			if c.Product == specref.Flows.Name {
				return specref.FetchFlows(ctx, specref.FlowsFetchOptions{
					Dir: c.Dir, Codeload: c.Codeload, NPM: c.NPM,
					ProviderRepo: c.FlowsProviderRepo, ProviderRef: c.FlowsProviderRef,
					FlowctlRepo: c.FlowctlRepo, FlowctlRef: c.FlowctlRef,
					SDKPackage: c.SDKPackage, SDKVersion: c.SDKVersion,
					Templates: c.Templates, Registry: c.Registry,
					Fetcher: fetcher, Now: time.Now,
				})
			}
			return specref.Fetch(ctx, specref.FetchOptions{
				Dir: c.Dir, Endpoint: c.Endpoint, Codeload: c.Codeload,
				BrunoRepo: c.BrunoRepo, BrunoRef: c.BrunoRef,
				ProviderRepo: c.ProviderRepo, ProviderVersion: c.ProviderVersion,
				Terraform: c.Terraform, Run: specref.ExecRunner, Now: time.Now,
				Fetcher: fetcher,
			})
		},
	}
	f := fetch.Flags()
	f.String("endpoint", defaultEndpoint, "GraphQL endpoint to introspect (needs no credentials)")
	f.String("codeload", "https://codeload.github.com", "base URL serving <repo>/tar.gz/<ref>")
	f.String("bruno-repo", defaultBrunoRepo, "spacelift-api-bruno repository")
	f.String("bruno-ref", defaultBrunoRef, "spacelift-api-bruno commit SHA")
	f.String("provider-repo", defaultProviderRepo, "terraform-provider-spacelift repository")
	f.String(
		"provider-version",
		defaultProviderVersion,
		"terraform-provider-spacelift release, without the v",
	)
	f.String("terraform", "terraform", "terraform binary used to read the provider schema")
	f.String(
		"flows-provider-repo",
		defaultFlowsProviderRepo,
		"terraform-provider-flows repository (flows)",
	)
	f.String(
		"flows-provider-ref",
		defaultFlowsProviderRef,
		"terraform-provider-flows commit SHA (flows)",
	)
	f.String("flowctl-repo", defaultFlowctlRepo, "flowctl repository (flows)")
	f.String("flowctl-ref", defaultFlowctlRef, "flowctl commit SHA (flows)")
	f.String("sdk-package", defaultSDKPackage, "the Flows app SDK's npm package (flows)")
	f.String("sdk-version", defaultSDKVersion, "the Flows app SDK's version (flows)")
	f.String("npm", "https://registry.npmjs.org", "npm registry serving the SDK tarball (flows)")
	f.String("templates", defaultTemplates, "the flow template index to validate (flows)")
	f.String("registry", defaultRegistry, "an app registry manifest to validate (flows)")
	f.String("timeout", "10m", "overall fetch timeout")

	generate := &cobra.Command{
		Use:   "generate",
		Short: "Write the product's OpenAPI reference and coverage.md from the snapshots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			p, err := specref.ProductNamed(c.Product)
			if err != nil {
				return err
			}
			g, err := p.GenerateDir(cmd.Context(), c.Dir)
			if err != nil {
				return err
			}
			cmd.Println(g.Summary)
			return nil
		},
	}
	check := &cobra.Command{
		Use:   "check",
		Short: "Fail if the committed spec or report is stale",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			p, err := specref.ProductNamed(c.Product)
			if err != nil {
				return err
			}
			return p.CheckDir(cmd.Context(), c.Dir)
		},
	}
	scaffold := &cobra.Command{
		Use:   "scaffold",
		Short: "Copy one reference tag into a served-API fragment, applying decision 0003",
		Long: "Copies every path holding an operation with the tag, and the components they\n" +
			"reach, into a fragment. Components and paths the other fragments in the output's\n" +
			"directory define are referenced, not copied. Reference operations without a tag\n" +
			"can't be scaffolded this way; add them to a fragment by hand.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			p, err := specref.ProductNamed(c.Product)
			if err != nil {
				return err
			}
			s, err := specref.ScaffoldFile(filepath.Join(c.Dir, p.Spec), specref.ActionCatalogue,
				c.Tag, c.Out, c.Force)
			if err != nil {
				return err
			}
			cmd.Printf("%s: %d operations, %d components\n", c.Out, s.Operations, s.Components)
			for _, p := range s.Shared {
				cmd.Printf("left out %s: another fragment defines it\n", p)
			}
			for _, f := range s.Foreign {
				cmd.Printf("carried %s: it shares a path with a %s operation\n", f, c.Tag)
			}
			cmd.Printf(
				"info.yaml needs a %q tag; the reference describes it as:\n%s\n",
				c.Tag,
				s.TagDescription,
			)
			return nil
		},
	}
	sf := scaffold.Flags()
	sf.String("tag", "", "reference tag to scaffold, such as Stacks")
	sf.String("out", "", "fragment to write, such as api/src/api/paths-stacks.yaml")
	sf.Bool("force", false, "overwrite an existing fragment")
	inventory := &cobra.Command{
		Use:   "inventory",
		Short: "Write the product's inventory register (and Deploy's report 29) from its snapshots",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			p, err := specref.ProductNamed(c.Product)
			if err != nil {
				return err
			}
			out := specref.InventoryOutputs{Report: c.Out, Register: c.Register}
			return p.Inventory(c.Dir, out, c.Check)
		},
	}
	inf := inventory.Flags()
	inf.String(
		"out",
		"",
		"report to write (default the product's, such as "+specref.Deploy.Report+")",
	)
	inf.String(
		"register",
		"",
		"register to write (default the product's, such as "+specref.Deploy.Register+")",
	)
	inf.Bool("check", false, "fail if the report or register is stale instead of writing them")
	parity := &cobra.Command{
		Use:   "parity",
		Short: "Decide each reference operation's outcome, and with --states each capability's state",
		Long: "Matches each reference operation with the served operation that has its\n" +
			"operationId, or the name docs/data/renames.json gives it, and decides decision\n" +
			"0016's outcome: served, served with divergence, superseded (a superseded record\n" +
			"of docs/data/supersessions.json lists it) or missing. A served operation that\n" +
			"differs in method, route or parameters without recording the difference in\n" +
			"x-infraweave-divergence fails, as does a superseded operation that is served.\n" +
			"Only the served operations under the product's own tags are compared. It prints\n" +
			"the counts by tag (and with --list every operation), then how the report differs\n" +
			"from the baseline. --states computes each record's state of\n" +
			"docs/data/capabilities.json from the acceptance results in --cucumber. --check\n" +
			"fails on a rule broken, a baseline difference or, with --states, a record that\n" +
			"claims more than the results show. --write-baseline rewrites the baseline to\n" +
			"the computed missing list, keeping each recorded reason.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			p, err := specref.ProductNamed(c.Product)
			if err != nil {
				return err
			}
			files := specref.ParityFiles{
				Reference:     filepath.Join(c.Dir, p.Spec),
				Served:        c.Served,
				Baseline:      c.Baseline,
				Renames:       c.Renames,
				Supersessions: c.Supersessions,
				Product:       p,
			}
			if c.WriteBaseline {
				b, err := specref.WriteParityBaseline(files)
				if err != nil {
					return err
				}
				missing := 0
				for _, ids := range b.Missing {
					missing += len(ids)
				}
				cmd.Printf("%s: %d missing\n", c.Baseline, missing)
				return nil
			}
			res, err := specref.ParityCheck(files)
			if err != nil {
				return err
			}
			cmd.Printf("%s:\n%s", p.Name, res.Report.Text())
			if c.List {
				cmd.Print(res.Report.Listing())
			}
			cmd.Printf("\nAgainst %s:\n%s", c.Baseline, res.Diff.Text())
			passed := res.Passed()
			if c.States {
				claims, err := states(cmd, c, p, res.Report)
				if err != nil {
					return err
				}
				passed = passed && len(claims) == 0
			}
			if c.Check && !passed {
				return errParity
			}
			return nil
		},
	}
	pyf := parity.Flags()
	pyf.String(
		"served",
		filepath.Join("api", "api.openapi.yaml"),
		"served API document to compare",
	)
	pyf.String(
		"baseline",
		"",
		"accepted missing operations (default the product's, such as "+
			specref.Deploy.Baseline+")",
	)
	pyf.String(
		"renames",
		filepath.Join("docs", "data", "renames.json"),
		"decision 0001's rename table",
	)
	pyf.String(
		"supersessions",
		filepath.Join("docs", "data", "supersessions.json"),
		"the supersession register, whose operations members list superseded operations",
	)
	pyf.Bool("states", false, "compute each capability record's state from the acceptance results")
	pyf.String(
		"cucumber",
		filepath.Join("cover", "accept", "cucumber.json"),
		"godog's cucumber JSON report of the last acceptance run; absent, nothing is evidenced",
	)
	pyf.String(
		"capabilities",
		filepath.Join("docs", "data", "capabilities.json"),
		"the capabilities register whose states --states checks",
	)
	pyf.Bool(
		"check",
		false,
		"fail when a rule is broken, the baseline differs or a state claims more",
	)
	pyf.Bool("list", false, "print every operation with its outcome")
	pyf.Bool(
		"write-baseline",
		false,
		"rewrite the baseline to accept the report instead of printing it",
	)
	actions := &cobra.Command{
		Use:   "actions",
		Short: "Write Deploy's action catalogue, actions.gen.go, from the Action enum and actions.yaml",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			if c.Product != specref.Deploy.Name {
				return fmt.Errorf("%w: only %s has an Action enum", specref.ErrUnknownProduct,
					specref.Deploy.Name)
			}
			return specref.ActionsFile(c.Dir, c.Catalogue, c.Generated, c.Check)
		},
	}
	af := actions.Flags()
	af.String("catalogue", specref.ActionCatalogue, "the hand-maintained catalogue rows")
	af.String("generated", specref.ActionsGo, "the generated file")
	af.Bool("check", false, "fail if the generated file is stale instead of writing it")
	root.AddCommand(fetch, generate, check, scaffold, inventory, parity, actions)
	return root
}

// states prints the computed state of each of the product's capability
// records and returns the records that claim more than it.
func states(
	cmd *cobra.Command,
	c *config,
	p specref.Product,
	rep *specref.ParityReport,
) ([]string, error) {
	caps, err := os.ReadFile(c.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("states: %w", err)
	}
	records, err := specref.ReadCapabilities(caps, p.Name)
	if err != nil {
		return nil, err
	}
	var scenarios []specref.Scenario
	data, err := os.ReadFile(c.Cucumber)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cmd.Printf("\n%s does not exist, so no scenario evidences an operation.\n", c.Cucumber)
	case err != nil:
		return nil, fmt.Errorf("states: %w", err)
	default:
		if scenarios, err = specref.ReadCucumber(data); err != nil {
			return nil, err
		}
	}
	var tf *specref.TerraformOperations
	if p.TerraformCalls != "" {
		data, err := os.ReadFile(filepath.Join(c.Dir, p.TerraformCalls))
		if err != nil {
			return nil, fmt.Errorf("states: %w", err)
		}
		if tf, err = specref.ReadTerraformOperations(data); err != nil {
			return nil, err
		}
	}
	st := rep.States(records, scenarios, tf)
	claims := specref.StateClaims(st)
	cmd.Printf("\nStates of %d %s records in %s, from %d scenarios:\n",
		len(st), p.Name, c.Capabilities, len(scenarios))
	if c.List {
		cmd.Print(specref.StatesText(st))
	}
	for _, s := range claims {
		cmd.Printf("claims more: %s\n", s)
	}
	if len(claims) == 0 {
		cmd.Println("No record claims more than the acceptance results show.")
	}
	return claims, nil
}

// loadConfig layers settings flag > env > file > lock pins > defaults.
func loadConfig(v *viper.Viper, cmd *cobra.Command, cfgFile string) error {
	// Bind settings only: --config, --help and --version are not settings,
	// and UnmarshalExact would reject them as unknown keys.
	var bindErr error
	cmd.Flags().VisitAll(func(fl *pflag.Flag) {
		if fl.Name == "config" || fl.Name == "help" || fl.Name == "version" {
			return
		}
		if err := v.BindPFlag(fl.Name, fl); err != nil {
			bindErr = errors.Join(bindErr, err)
		}
	})
	if bindErr != nil {
		return fmt.Errorf("bind flags: %w", bindErr)
	}
	v.SetEnvPrefix("SPECREF")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read config: %w", err)
		}
	}
	p, err := specref.ProductNamed(v.GetString("product"))
	if err != nil {
		return err
	}
	// Viper ranks SetDefault under a changed flag, env and the config file
	// but above an unchanged flag's own default, so the product and its lock
	// fill exactly what nobody set explicitly.
	v.SetDefault("dir", filepath.FromSlash(p.Dir))
	v.SetDefault("baseline", filepath.FromSlash(p.Baseline))
	v.SetDefault("register", filepath.FromSlash(p.Register))
	v.SetDefault("out", filepath.FromSlash(p.Report))
	pins, err := p.Pins(v.GetString("dir"))
	if err != nil {
		return err
	}
	for key, value := range pins {
		v.SetDefault(key, value)
	}
	return nil
}

// decode applies the layered settings strictly: a config file key that
// matches no setting is an error, not a silent no-op.
func decode(v *viper.Viper) (*config, error) {
	var c config
	if err := v.UnmarshalExact(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}
