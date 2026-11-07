package ctl

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// op is path under the operator API.
func op(path string) string { return sdk.OperatorPrefix + path }

// run is a RunE that sends method to the path args make, with no body.
func run(method string, path func(args []string) string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return call(cmd, method, op(path(args)), nil)
	}
}

func operatorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Manage the installation through the operator API, with an operator key",
	}
	cmd.AddCommand(tierCmd(), organizationCmd(), grantCmd(), billingCmd())
	return cmd
}

func tierCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tier",
		Short: "Manage tiers, the features and limits an organization holds",
	}
	content := func(cmd *cobra.Command) map[string]any {
		features, _ := cmd.Flags().GetStringSlice("feature")
		limits, _ := cmd.Flags().GetStringToInt64("limit")
		// The API requires both, so an absent flag is an empty list or map,
		// never null.
		if features == nil {
			features = []string{}
		}
		if limits == nil {
			limits = map[string]int64{}
		}
		return map[string]any{"features": features, "limits": limits}
	}
	contentFlags := func(c *cobra.Command) *cobra.Command {
		c.Flags().StringSlice("feature", nil, "a gated feature the tier holds, repeatable")
		c.Flags().StringToInt64("limit", nil, "a limit's value, NAME=VALUE, repeatable")
		return c
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List the tiers", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return list(cmd, op("/tiers")) },
		},
		&cobra.Command{
			Use: "get NAME", Short: "Read a tier", Args: cobra.ExactArgs(1),
			RunE: run(http.MethodGet, func(a []string) string { return "/tiers/" + seg(a[0]) }),
		},
		contentFlags(&cobra.Command{
			Use: "create NAME", Short: "Create a tier", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				body := content(cmd)
				body["name"] = args[0]
				return call(cmd, http.MethodPost, op("/tiers"), body)
			},
		}),
		contentFlags(&cobra.Command{
			Use:   "update NAME",
			Short: "Replace a tier's features and limits; Enterprise cannot be edited",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return call(cmd, http.MethodPut, op("/tiers/"+seg(args[0])), content(cmd))
			},
		}),
		&cobra.Command{
			Use:   "delete NAME",
			Short: "Delete a tier no organization holds and billing does not default to",
			Args:  cobra.ExactArgs(1),
			RunE:  run(http.MethodDelete, func(a []string) string { return "/tiers/" + seg(a[0]) }),
		},
	)
	return cmd
}

func organizationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "organization", Short: "Manage organizations"}
	org := func(a []string) string { return "/organizations/" + seg(a[0]) }
	create := &cobra.Command{
		Use:   "create SLUG",
		Short: "Create an organization and its first account, on the default tier",
		Long: "Create an organization and its first account, on the default tier. With " +
			"--break-glass-login, standard input holds the break-glass password's hash, as " +
			"infraweave auth hash-password prints it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			name, _ := f.GetString("name")
			acctSlug, _ := f.GetString("account-slug")
			acctName, _ := f.GetString("account-name")
			login, _ := f.GetString("break-glass-login")
			body := map[string]any{
				"slug": args[0], "name": name,
				"account": map[string]string{"slug": acctSlug, "name": acctName},
			}
			if login != "" {
				hash, err := stdin(cmd, "password hash")
				if err != nil {
					return err
				}
				body["breakGlass"] = map[string]string{"login": login, "passwordHash": hash}
			}
			return call(cmd, http.MethodPost, op("/organizations"), body)
		},
	}
	create.Flags().String("name", "", "the organization's display name")
	create.Flags().String("account-slug", "", "the first account's slug")
	create.Flags().String("account-name", "", "the first account's display name")
	create.Flags().String("break-glass-login", "", "the break-glass admin's login")
	for _, f := range []string{"name", "account-slug", "account-name"} {
		// MarkFlagRequired fails only for a flag that does not exist.
		_ = create.MarkFlagRequired(f)
	}

	breakGlass := &cobra.Command{
		Use:   "break-glass",
		Short: "Manage an organization's break-glass credential",
	}
	set := &cobra.Command{
		Use:   "set SLUG",
		Short: "Replace the break-glass credential; standard input holds the password's hash",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			login, _ := cmd.Flags().GetString("login")
			hash, err := stdin(cmd, "password hash")
			if err != nil {
				return err
			}
			return call(cmd, http.MethodPut, op(org(args)+"/break-glass"),
				map[string]string{"login": login, "passwordHash": hash})
		},
	}
	set.Flags().String("login", "", "the break-glass admin's login")
	_ = set.MarkFlagRequired("login")
	breakGlass.AddCommand(set, &cobra.Command{
		Use: "delete SLUG", Short: "Remove the break-glass credential", Args: cobra.ExactArgs(1),
		RunE: run(http.MethodDelete, func(a []string) string { return org(a) + "/break-glass" }),
	})

	tier := &cobra.Command{Use: "tier", Short: "Manage an organization's tier"}
	tier.AddCommand(&cobra.Command{
		Use: "set SLUG TIER", Short: "Give an organization a tier", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return call(
				cmd,
				http.MethodPut,
				op(org(args)+"/tier"),
				map[string]string{"tier": args[1]},
			)
		},
	})

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List the organizations",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, _ []string) error { return list(cmd, op("/organizations")) },
		},
		&cobra.Command{
			Use: "get SLUG", Short: "Read an organization", Args: cobra.ExactArgs(1),
			RunE: run(http.MethodGet, org),
		},
		create,
		&cobra.Command{
			Use:   "delete SLUG",
			Short: "Delete an organization and all its accounts",
			Args:  cobra.ExactArgs(1),
			RunE:  run(http.MethodDelete, org),
		},
		breakGlass,
		tier,
	)
	return cmd
}

func grantCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "grant", Short: "Manage an organization's grants beyond its tier"}
	grants := func(a []string, kind string) string {
		return "/organizations/" + seg(a[0]) + "/grants/" + kind + "/" + seg(a[1])
	}
	feature := &cobra.Command{Use: "feature", Short: "Grant or revoke a feature"}
	feature.AddCommand(
		&cobra.Command{
			Use: "set SLUG FEATURE", Short: "Grant a feature", Args: cobra.ExactArgs(2),
			RunE: run(http.MethodPut, func(a []string) string { return grants(a, "features") }),
		},
		&cobra.Command{
			Use: "remove SLUG FEATURE", Short: "Revoke a granted feature", Args: cobra.ExactArgs(2),
			RunE: run(http.MethodDelete, func(a []string) string { return grants(a, "features") }),
		},
	)
	limit := &cobra.Command{Use: "limit", Short: "Grant or revoke a limit's value"}
	limit.AddCommand(
		&cobra.Command{
			Use:   "set SLUG LIMIT VALUE",
			Short: "Grant a limit's value, a whole number or unlimited",
			Args:  cobra.ExactArgs(3),
			RunE: func(cmd *cobra.Command, args []string) error {
				var value *int64
				if args[2] != "unlimited" {
					n, err := strconv.ParseInt(args[2], 10, 64)
					if err != nil {
						return fmt.Errorf(
							"%w: limit value %q is neither a whole number nor unlimited",
							ErrInput,
							args[2],
						)
					}
					value = &n
				}
				return call(
					cmd,
					http.MethodPut,
					op(grants(args, "limits")),
					map[string]*int64{"value": value},
				)
			},
		},
		&cobra.Command{
			Use: "remove SLUG LIMIT", Short: "Revoke a granted limit", Args: cobra.ExactArgs(2),
			RunE: run(http.MethodDelete, func(a []string) string { return grants(a, "limits") }),
		},
	)
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list SLUG",
			Short: "Print the organization's tier, grants and what they entitle it to",
			Args:  cobra.ExactArgs(1),
			RunE: run(http.MethodGet, func(a []string) string {
				return "/organizations/" + seg(a[0]) + "/entitlement"
			}),
		},
		feature,
		limit,
	)
	return cmd
}

func billingCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "billing", Short: "Manage the billing switch and the default tier"}
	set := &cobra.Command{
		Use:   "set",
		Short: "Turn entitlement checks on or off, and set the default tier",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			enabled, _ := cmd.Flags().GetBool("enabled")
			body := map[string]any{"enabled": enabled}
			if t, _ := cmd.Flags().GetString("default-tier"); t != "" {
				body["defaultTier"] = t
			}
			return call(cmd, http.MethodPut, op("/billing"), body)
		},
	}
	set.Flags().Bool("enabled", false, "whether entitlement checks run")
	set.Flags().
		String("default-tier", "", "the tier a new organization gets; left out, it stays as it is")
	_ = set.MarkFlagRequired("enabled")
	cmd.AddCommand(&cobra.Command{
		Use: "get", Short: "Read the billing switch and the default tier", Args: cobra.NoArgs,
		RunE: run(http.MethodGet, func([]string) string { return "/billing" }),
	}, set)
	return cmd
}
