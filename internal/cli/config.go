package cli

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
	"github.com/datalens-tech/datalens-cli/internal/schemacache"
	"github.com/spf13/cobra"
)

func (a *app) profilesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "profiles", Short: "Manage installation profiles", Args: groupArgs,
		RunE: showHelp,
	}
	cmd.AddCommand(a.createProfileCommand(), a.listProfilesCommand(), a.activateProfileCommand(), a.deleteProfileCommand())
	return cmd
}

func profileNameArg(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("profile") {
		return usageError("Pass the profile name as a positional argument and omit --profile.")
	}
	if len(args) == 0 {
		path := cmd.CommandPath()
		return usageError(fmt.Sprintf("Missing profile name. Example: %s work\nSee '%s --help' for options.", path, path))
	}
	return exactArgs(1)(cmd, args)
}

func (a *app) createProfileCommand() *cobra.Command {
	var profile config.Profile
	var account config.ServiceAccount
	var tokenCommand string
	cmd := &cobra.Command{
		Use: "create NAME", Short: "Create an installation profile", Args: profileNameArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			store, err := a.store()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("token-cmd") {
				profile.TokenCommand, err = splitTokenCommand(tokenCommand)
				if err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("service-account-id") || cmd.Flags().Changed("key-id") || cmd.Flags().Changed("private-key-file") {
				profile.ServiceAccount = &account
			}
			profile, err = config.NormalizeProfile(profile)
			if err != nil {
				return err
			}
			if err := store.Create(cmd.Context(), name, profile); err != nil {
				return err
			}
			return a.profileCreated(cmd.Context(), profileContext{Profile: profile, name: name, dir: store.Dir}, fmt.Sprintf("Created profile %s.\n", name))
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&profile.Installation, "installation-type", "", "Installation type: cloud, yateam, on-premises, or stackland")
	flags.StringVar(&profile.Endpoint, "endpoint", "", "DataLens UI endpoint")
	flags.StringVar(&profile.APIEndpoint, "api-endpoint", "", "Override the API endpoint derived from the UI endpoint")
	flags.StringVar(&profile.OrganizationID, "organization", "", "Organization ID for Cloud or Stackland")
	flags.StringVar(&tokenCommand, "token-cmd", "", "Run a program directly to obtain the token (quote arguments containing spaces)")
	flags.StringVar(&account.ID, "service-account-id", "", "On-premises service account ID")
	flags.StringVar(&account.KeyID, "key-id", "", "ID of the service account signing key")
	flags.StringVar(&account.PrivateKeyFile, "private-key-file", "", "Path to the service account's PEM private key file")
	return cmd
}

func (a *app) profileCreated(ctx context.Context, p profileContext, message string) error {
	result := map[string]string{"name": p.name}
	if err := schemacache.Update(ctx, p.dir, p.Profile, a.options.Transport); err != nil {
		warning := fmt.Sprintf("Could not download the RPC schema. %v Run 'datalens schema update --profile=%s' to retry.", err, p.name)
		result["schemaWarning"] = warning
		message += warning + "\n"
	}
	return a.writer.Result(result, message)
}

func splitTokenCommand(command string) ([]string, error) {
	args := []string{}
	for len(command) > 0 {
		command = strings.TrimLeft(command, " \t")
		if command == "" {
			break
		}
		var arg strings.Builder
		for len(command) > 0 && command[0] != ' ' && command[0] != '\t' {
			quote := command[0]
			if quote != '\'' && quote != '"' {
				arg.WriteByte(quote)
				command = command[1:]
				continue
			}
			command = command[1:]
			end := strings.IndexByte(command, quote)
			if end < 0 {
				return nil, usageError("Unclosed quote in --token-cmd.")
			}
			arg.WriteString(command[:end])
			command = command[end+1:]
		}
		args = append(args, arg.String())
	}
	return args, nil
}

type profileSummary struct {
	Name            string            `json:"name"`
	Installation    string            `json:"installationType,omitempty"`
	Endpoint        string            `json:"endpoint,omitempty"`
	APIEndpoint     string            `json:"apiEndpoint,omitempty"`
	OrganizationID  string            `json:"organizationId,omitempty"`
	HasTokenCommand bool              `json:"hasTokenCommand,omitempty"`
	ServiceAccount  map[string]string `json:"serviceAccount,omitempty"`
	Problem         string            `json:"problem,omitempty"`
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
			var selected string
			var text strings.Builder
			if len(document.Profiles) == 0 {
				text.WriteString(config.NoProfilesMessage + "\n")
			} else {
				selected, err = document.SelectedName(a.profile, a.options.LookupEnv("DATALENS_PROFILE"))
				if err != nil {
					return err
				}
			}
			profiles := make([]profileSummary, 0, len(document.Profiles))
			for _, name := range slices.Sorted(maps.Keys(document.Profiles)) {
				profile := document.Profiles[name]
				problem := profile.Problem(name)
				summary := profileSummary{Name: name, Problem: problem}
				if problem == "" {
					summary.Installation, summary.Endpoint, summary.APIEndpoint = profile.Installation, profile.Endpoint, profile.APIEndpoint
					summary.OrganizationID = profile.OrganizationID
					summary.HasTokenCommand = len(profile.TokenCommand) > 0
					if sa := profile.ServiceAccount; sa != nil {
						summary.ServiceAccount = map[string]string{"id": sa.ID, "keyId": sa.KeyID}
					}
				}
				profiles = append(profiles, summary)
				marker := " "
				if name == selected {
					marker = "*"
				}
				if problem != "" {
					fmt.Fprintf(&text, "%s %s\tinvalid: %s\n", marker, displayProfileName(name), problem)
				} else {
					fmt.Fprintf(&text, "%s %s\t%s\t%s\n", marker, name, profile.Installation, profile.Endpoint)
				}
			}
			return a.writer.Result(struct {
				Profiles        []profileSummary `json:"profiles"`
				DefaultProfile  string           `json:"defaultProfile"`
				SelectedProfile string           `json:"selectedProfile"`
			}{profiles, document.CurrentProfile, selected}, text.String())
		},
	}
}

func displayProfileName(name string) string {
	if config.ValidProfileName(name) {
		return name
	}
	return strconv.QuoteToASCII(name)
}

func (a *app) activateProfileCommand() *cobra.Command {
	return &cobra.Command{
		Use: "activate NAME", Short: "Use a profile by default", Args: profileNameArg,
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
		Use: "delete NAME", Short: "Delete a local profile", Args: profileNameArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			hasConfirm := cmd.Flags().Changed("confirm")
			if hasConfirm && confirm != "yes" {
				return usageError("use --confirm=yes to confirm deletion")
			}
			store, err := a.store()
			if err != nil {
				return err
			}
			doc, err := store.Load()
			if err != nil {
				return err
			}
			if err := doc.CheckDeletable(name); err != nil {
				return err
			}
			ctx := cmd.Context()
			var expected *config.SavedProfile
			if !hasConfirm {
				profile := doc.Profiles[name]
				expected = &profile
				if err := a.confirmDeletion(ctx, name); err != nil {
					return err
				}
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, a.timeout)
				defer cancel()
			}
			empty, err := store.Delete(ctx, name, expected)
			if err != nil {
				return err
			}
			message := fmt.Sprintf("Deleted profile %s.\n", displayProfileName(name))
			if empty {
				message += config.NoProfilesMessage + "\n"
			}
			return a.writer.Result(map[string]string{"name": name}, message)
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
