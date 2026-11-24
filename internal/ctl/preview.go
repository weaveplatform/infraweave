package ctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// previewAnnotation is the annotation key whose value names the feature
// preview that gates a command and every command below it. The root hides
// such a command from help and refuses to run it unless featurePreview
// answers the feature active for the caller, so a member who opted out,
// or a member of an account that has not opted in, does not see it.
//
// ref: decision 0021
const previewAnnotation = "infraweave/preview"

// ErrPreviewInactive is a command a feature preview gates, run while the
// preview is not active for the caller.
var ErrPreviewInactive = errors.New("feature preview not active")

// The operations of the preview group, under the API's prefix.
const (
	previewPath         = sdk.APIPrefix + "/feature-preview"
	previewAccountPath  = sdk.APIPrefix + "/account/set-feature-previews-enabled"
	previewPersonalPath = sdk.APIPrefix + "/feature-preview/personal"
)

func previewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Read the account's feature previews and set its opt-in or your own",
		Long: "Read the account's feature previews and set the account's opt-in, which " +
			"lets a feature in preview reach the account, or your own setting, which only " +
			"shows or hides previews in your clients.",
	}
	setting := func(method, path, use string, enabled bool, short, operation string) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Long:  short + ", and print the account's feature previews (" + operation + ").",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return call(cmd, method, path, map[string]bool{"enabled": enabled})
			},
		}
	}
	account := &cobra.Command{
		Use:   "account",
		Short: "Opt the account in to feature previews or out of them, as an account admin",
	}
	account.AddCommand(
		setting(http.MethodPost, previewAccountPath, "enable", true,
			"Opt the account in to feature previews", "accountSetFeaturePreviewsEnabled"),
		setting(http.MethodPost, previewAccountPath, "disable", false,
			"Opt the account out of feature previews; their data stays",
			"accountSetFeaturePreviewsEnabled"),
	)
	personal := &cobra.Command{
		Use:   "personal",
		Short: "Show feature previews in your own clients or hide them",
	}
	personal.AddCommand(
		setting(http.MethodPut, previewPersonalPath, "enable", true,
			"Show feature previews in your own clients", "featurePreviewSetPersonalEnabled"),
		setting(http.MethodPut, previewPersonalPath, "disable", false,
			"Hide feature previews in your own clients", "featurePreviewSetPersonalEnabled"),
	)
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the feature previews, the account's opt-in and your own setting",
		Long: "Print every feature in preview the installation has not switched off, " +
			"whether it is active for you, the account's opt-in and whether you may " +
			"change it, and your own setting when you are a member (featurePreview).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return call(cmd, http.MethodGet, previewPath, nil)
		},
	}, account, personal)
	return cmd
}

// gatingPreview is the feature preview that gates c, named by its own
// annotation or its nearest ancestor's, or "" for none.
func gatingPreview(c *cobra.Command) string {
	for ; c != nil; c = c.Parent() {
		if f := c.Annotations[previewAnnotation]; f != "" {
			return f
		}
	}
	return ""
}

// gate refuses a command a feature preview gates while featurePreview
// answers the preview inactive for the caller. A command no preview gates
// runs without a call.
func gate(cmd *cobra.Command, _ []string) error {
	feature := gatingPreview(cmd)
	if feature == "" {
		return nil
	}
	active, err := activePreviews(cmd, cmd.OutOrStdout())
	if err != nil {
		return err
	}
	if !active[feature] {
		return fmt.Errorf("%w: %s belongs to the feature preview %s; an account admin opts "+
			"the account in with infraweavectl preview account enable, and a member who opted "+
			"out opts back in with infraweavectl preview personal enable",
			ErrPreviewInactive, cmd.CommandPath(), feature)
	}
	return nil
}

// hideInactive hides every command of the tree a feature preview gates
// whose preview featurePreview does not answer active for the caller. It
// calls the server only when the tree holds such a command, and hides
// every one of them when the call fails, so help never fails and never
// lists a command it cannot confirm.
func hideInactive(cmd *cobra.Command) {
	var gated []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Annotations[previewAnnotation] != "" {
			gated = append(gated, c)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(cmd.Root())
	if len(gated) == 0 {
		return
	}
	// A failed call leaves active empty, which hides them all.
	active, _ := activePreviews(cmd, io.Discard)
	for _, c := range gated {
		c.Hidden = !active[c.Annotations[previewAnnotation]]
	}
}

// activePreviews is the set of features featurePreview answers active for
// the caller. A refusal prints its problem to w and returns the SDK's
// typed error.
func activePreviews(cmd *cobra.Command, w io.Writer) (map[string]bool, error) {
	// The root's persistent flags hold the settings whichever command
	// parsed them, including a help command's target, which parses none.
	root := cmd.Root()
	c, err := client(root.PersistentFlags())
	if err != nil {
		return nil, err
	}
	r, err := c.Do(root.Context(), http.MethodGet, previewPath, nil)
	if err != nil {
		if perr := show(w, answer(r, err)); perr != nil {
			return nil, perr
		}
		return nil, err //nolint:wrapcheck // the SDK's typed problem, as it is
	}
	var fp struct {
		Features []struct {
			Key    string `json:"key"`
			Active bool   `json:"active"`
		} `json:"features"`
	}
	if err := json.Unmarshal(r.Body, &fp); err != nil {
		return nil, fmt.Errorf("decode %s: %w", previewPath, err)
	}
	active := make(map[string]bool, len(fp.Features))
	for _, f := range fp.Features {
		active[f.Key] = f.Active
	}
	return active, nil
}
