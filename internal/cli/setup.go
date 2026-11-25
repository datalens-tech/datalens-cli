package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/spf13/cobra"
)

func (a *app) setupCommand() *cobra.Command {
	return &cobra.Command{
		Use: "setup", Short: "Create and activate a profile interactively", Args: exactArgs(0),
		Long: "Choose an installation and how to supply its token. Review the settings and confirm to create and activate the profile. For scripts, use datalens profiles create and datalens profiles activate.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.nonInteractive || a.format == "json" {
				return usageError("Run 'datalens setup' in an interactive terminal with text output. For scripts, use 'datalens profiles create' and 'datalens profiles activate'.")
			}
			if cmd.Flags().Changed("profile") || cmd.Flags().Changed("auth-token") {
				return usageError("Setup creates a new profile and asks how to obtain a token. Omit --profile and --auth-token.")
			}
			return a.runSetup(cmd.Context())
		},
	}
}

type setupPrompt struct {
	ctx context.Context
	in  *bufio.Scanner
	out io.Writer
}

func (p setupPrompt) line(question string) (string, error) {
	if _, err := fmt.Fprint(p.out, question); err != nil {
		return "", err
	}
	scanned := make(chan bool, 1)
	go func() { scanned <- p.in.Scan() }()
	select {
	case <-p.ctx.Done():
		return "", context.Cause(p.ctx)
	case ok := <-scanned:
		if !ok {
			if p.in.Err() != nil {
				return "", usageError("Could not read setup input.")
			}
			return "", usageError("Setup cancelled before the profile was saved.")
		}
		return strings.TrimSpace(p.in.Text()), context.Cause(p.ctx)
	}
}

func (p setupPrompt) choice(title string, items ...string) (int, error) {
	if _, err := fmt.Fprintln(p.out, title); err != nil {
		return 0, err
	}
	for i, label := range items {
		if _, err := fmt.Fprintf(p.out, "  %d. %s\n", i+1, label); err != nil {
			return 0, err
		}
	}
	prompt := "Choice [1]: "
	for {
		value, err := p.line(prompt)
		if err != nil || value == "" {
			return 0, err
		}
		if n, err := strconv.Atoi(value); err == nil && n >= 1 && n <= len(items) {
			return n - 1, nil
		}
		prompt = fmt.Sprintf("Enter a number from 1 to %d [1]: ", len(items))
	}
}

func (p setupPrompt) auth(profile *config.Profile) (description, hint string, err error) {
	name := "DATALENS_TOKEN"
	if profile.Installation == "yateam" {
		name = "DATALENS_OAUTH_TOKEN"
	}
	environment := "Environment (" + name + " or DATALENS_TOKEN_PATH)"
	options := []string{environment, "Custom token command"}
	if profile.Installation == "cloud" {
		options = append([]string{"Yandex Cloud CLI (yc iam create-token)"}, options...)
	}
	n, err := p.choice("How should the CLI obtain a token?", options...)
	if err != nil {
		return "", "", err
	}
	if profile.Installation == "cloud" {
		if n == 0 {
			profile.TokenCommand = []string{"yc", "iam", "create-token"}
			return "Token command: yc iam create-token", "", nil
		}
		n--
	}
	if n == 0 {
		return environment, "Set " + name + " to your token or DATALENS_TOKEN_PATH to its file before making an API request.\n", nil
	}
	question := "Command that prints a token: "
	for {
		value, err := p.line(question)
		if err != nil {
			return "", "", err
		}
		args, err := splitTokenCommand(value)
		if err == nil {
			_, err = config.NormalizeProfile(config.Profile{TokenCommand: args})
		}
		if err == nil {
			profile.TokenCommand = args
			return "Token command: " + value, "", nil
		}
		question = "Enter an executable and its arguments. Use matching quotes around arguments with spaces: "
	}
}

func (a *app) runSetup(ctx context.Context) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	p := setupPrompt{ctx: ctx, in: bufio.NewScanner(a.options.In), out: a.options.Err}
	n, err := p.choice("Choose your DataLens installation:", "Yandex Cloud", "Yandex Team", "On-premises (standalone)", "Stackland")
	if err != nil {
		return err
	}
	profile := config.Profile{Installation: []string{"cloud", "yateam", "onpremise-standalone", "stackland"}[n]}
	authentication, hint, err := p.auth(&profile)
	if err != nil {
		return err
	}
	switch profile.Installation {
	case "cloud":
		profile.OrganizationID, err = p.line("Cloud organization ID (Enter to use DATALENS_ORG_ID): ")
		if err != nil {
			return err
		}
		if profile.OrganizationID == "" {
			hint += "Set DATALENS_ORG_ID before making a Cloud API request.\n"
		}
	case "onpremise-standalone", "stackland":
		question := "DataLens UI endpoint (HTTPS URL): "
		for {
			profile.Endpoint, err = p.line(question)
			if err != nil {
				return err
			}
			if _, err = config.NormalizeProfile(profile); err == nil {
				break
			}
			question = err.Error() + "\nDataLens UI endpoint: "
		}
	}

	profile, err = config.NormalizeProfile(profile)
	if err != nil {
		return err
	}
	defaultName := profile.Installation
	for {
		question := "Profile name: "
		if defaultName != "" {
			question = "Profile name [" + defaultName + "]: "
		}
		name, err := p.line(question)
		if err != nil {
			return err
		}
		if name == "" {
			name = defaultName
		}
		defaultName = ""
		if !config.ValidProfileName(name) {
			if _, err := fmt.Fprintln(p.out, "Use a profile name with letters, digits, dots, underscores, or hyphens."); err != nil {
				return err
			}
			continue
		}
		doc, err := store.Load()
		if err != nil {
			return err
		}
		if _, exists := doc.Profiles[name]; exists {
			if _, err := fmt.Fprintln(p.out, "A profile with this name already exists."); err != nil {
				return err
			}
			continue
		}
		apply, err := p.confirmProfile(name, profile, authentication)
		if err != nil {
			return err
		}
		if !apply {
			return a.writer.Result(nil, "Setup cancelled.\n")
		}
		err = store.CreateAndUse(ctx, name, profile)
		var problem *config.Error
		if errors.As(err, &problem) && problem.Code == "PROFILE_EXISTS" {
			if _, err = fmt.Fprintln(p.out, problem.Message); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if selected := a.options.LookupEnv("DATALENS_PROFILE"); selected != "" && selected != name {
			hint += "DATALENS_PROFILE overrides the active profile. Unset it to use this profile by default.\n"
		}
		return a.writer.Result(map[string]string{"name": name}, fmt.Sprintf("Created and activated profile %s.\n%s", name, hint))
	}
}

func (p setupPrompt) confirmProfile(name string, profile config.Profile, authentication string) (bool, error) {
	var summary strings.Builder
	fmt.Fprintf(&summary, "\nProfile name: %s\nInstallation type: %s\nUI endpoint: %s\nAPI endpoint: %s\n",
		name, profile.Installation, profile.Endpoint, profile.APIEndpoint)
	if profile.Installation == "cloud" {
		org := profile.OrganizationID
		if org == "" {
			org = "from DATALENS_ORG_ID"
		}
		fmt.Fprintf(&summary, "Cloud organization: %s\n", org)
	}
	fmt.Fprintf(&summary, "Authentication: %s\n", authentication)
	if _, err := io.WriteString(p.out, summary.String()); err != nil {
		return false, err
	}
	question := "Create and activate this profile? [Y/n] "
	for {
		answer, err := p.line(question)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		question = "Enter yes to create the profile or no to cancel [Y/n]: "
	}
}
