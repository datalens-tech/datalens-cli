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
			if err := store.Create(cmd.Context(), name, profile); err != nil {
				return err
			}
			return a.writer.Result(map[string]string{"name": name}, fmt.Sprintf("Created profile %s.\n", name))
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&profile.Installation, "installation-type", "", "Installation type: cloud, yateam, onpremise-standalone, or stackland")
	flags.StringVar(&profile.Endpoint, "endpoint", "", "DataLens UI endpoint")
	flags.StringVar(&profile.APIEndpoint, "api-endpoint", "", "Override the API endpoint derived from the UI endpoint")
	flags.StringVar(&profile.OrganizationID, "organization", "", "Cloud organization ID")
	flags.StringVar(&tokenCommand, "token-cmd", "", "Run a program directly to obtain the token (quote arguments containing spaces)")
	return cmd
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
	Name string `json:"name"`
	config.Profile
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
				selected, _, err = config.Resolve(document, a.profile, a.options.LookupEnv("DATALENS_PROFILE"))
				if err != nil {
					return err
				}
			}
			profiles := make([]profileSummary, 0, len(document.Profiles))
			for _, name := range slices.Sorted(maps.Keys(document.Profiles)) {
				profile := document.Profiles[name]
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
			}{profiles, document.CurrentProfile, selected}, text.String())
		},
	}
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
			if !hasConfirm {
				if err := a.confirmDeletion(cmd.Context(), name); err != nil {
					return err
				}
			}
			empty, err := store.Delete(cmd.Context(), name)
			if err != nil {
				return err
			}
			message := fmt.Sprintf("Deleted profile %s.\n", name)
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
