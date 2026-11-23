package cli

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
	"github.com/spf13/cobra"
)

func (a *app) profilesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "profiles", Short: "Manage installation profiles", Args: groupArgs,
		RunE: showHelp,
	}
	cmd.AddCommand(a.createProfileCommand(), a.updateProfileCommand(), a.listProfilesCommand(), a.activateProfileCommand(), a.deleteProfileCommand())
	return cmd
}

func (a *app) createProfileCommand() *cobra.Command {
	var name string
	var profile config.Profile
	cmd := &cobra.Command{
		Use: "create --name=NAME", Short: "Create an installation profile", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return usageError("--name is required")
			}
			store, err := a.store()
			if err != nil {
				return err
			}
			if err := store.Create(cmd.Context(), name, profile); err != nil {
				return err
			}
			return a.writer.Result(map[string]string{"name": name}, fmt.Sprintf("Created profile %s.\n", name))
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&name, "name", "", "Profile name (required)")
	flags.StringVar(&profile.Installation, "installation-type", "", "Installation type: cloud, yateam, onpremise-standalone, or stackland")
	flags.StringVar(&profile.Endpoint, "endpoint", "", "DataLens UI endpoint")
	flags.StringVar(&profile.APIEndpoint, "api-endpoint", "", "Override the API endpoint derived from the UI endpoint")
	flags.StringVar(&profile.OrganizationID, "organization", "", "Cloud organization ID")
	flags.StringVar(&profile.APIVersion, "api-version", "", "API version for this profile")
	return cmd
}

type profileSummary struct {
	Name string `json:"name"`
	config.Profile
}

func (a *app) updateProfileCommand() *cobra.Command {
	var organization string
	cmd := &cobra.Command{
		Use: "update NAME --organization=ID", Short: "Set the organization of a Cloud profile", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(organization) == "" {
				return usageError("--organization requires an organization ID")
			}
			store, err := a.store()
			if err != nil {
				return err
			}
			if err := store.SetOrganization(cmd.Context(), args[0], organization); err != nil {
				return err
			}
			return a.writer.Result(map[string]string{"name": args[0]}, fmt.Sprintf("Updated profile %s.\n", args[0]))
		},
	}
	cmd.Flags().StringVar(&organization, "organization", "", "Cloud organization ID (required)")
	return cmd
}

func (a *app) listProfilesCommand() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List profiles and mark the selected profile", Args: exactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := a.store()
			if err != nil {
				return err
			}
			document, err := store.Load()
			if err != nil {
				return err
			}
			selected, _, err := config.Resolve(document, a.profile, a.options.LookupEnv("DATALENS_PROFILE"))
			if err != nil {
				return err
			}
			effective := document.EffectiveProfiles()
			profiles := make([]profileSummary, 0, len(effective))
			var text strings.Builder
			for _, name := range slices.Sorted(maps.Keys(effective)) {
				profile := effective[name]
				profiles = append(profiles, profileSummary{Name: name, Profile: profile})
				marker := " "
				if name == selected {
					marker = "*"
				}
				fmt.Fprintf(&text, "%s %s\t%s\t%s\n", marker, name, profile.Installation, profile.Endpoint)
			}
			return a.writer.Result(struct {
				Profiles        []profileSummary `json:"profiles"`
				DefaultProfile  string           `json:"defaultProfile"`
				SelectedProfile string           `json:"selectedProfile"`
			}{profiles, document.ActiveName(), selected}, text.String())
		},
	}
}

func (a *app) activateProfileCommand() *cobra.Command {
	return &cobra.Command{
		Use: "activate NAME", Short: "Use a profile by default", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := a.store()
			if err != nil {
				return err
			}
			if err := store.Use(cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.writer.Result(map[string]string{"name": args[0]}, fmt.Sprintf("Activated profile %s.\n", args[0]))
		},
	}
}

func (a *app) deleteProfileCommand() *cobra.Command {
	var confirm string
	cmd := &cobra.Command{
		Use: "delete NAME", Short: "Delete a local profile", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("confirm") {
				if confirm != "yes" {
					return usageError("use --confirm=yes to confirm deletion")
				}
			} else if err := a.confirmDeletion(cmd.Context(), args[0]); err != nil {
				return err
			}
			store, err := a.store()
			if err != nil {
				return err
			}
			if err := store.Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.writer.Result(map[string]string{"name": args[0]}, fmt.Sprintf("Deleted profile %s.\n", args[0]))
		},
	}
	cmd.Flags().StringVar(&confirm, "confirm", "", "Pass yes to delete without a prompt")
	return cmd
}

func (a *app) confirmDeletion(ctx context.Context, name string) error {
	if a.nonInteractive || a.format == "json" {
		return &output.Error{Code: "CONFIRMATION_REQUIRED", Message: "pass --confirm=yes when deleting non-interactively or with --format=json", ExitCode: 2}
	}
	if _, err := fmt.Fprintf(a.options.Err, "Delete profile %q? [y/N] ", name); err != nil {
		return err
	}
	answer := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(a.options.In)
		if scanner.Scan() {
			switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
			case "y", "yes":
				answer <- true
				return
			}
		}
		answer <- false
	}()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case confirmed := <-answer:
		if !confirmed {
			return &output.Error{Code: "CONFIRMATION_DECLINED", Message: "profile deletion cancelled", ExitCode: 1}
		}
		return context.Cause(ctx)
	}
}
