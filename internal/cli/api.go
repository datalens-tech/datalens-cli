package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/datalens-tech/datalens-cli/internal/api"
	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
	"github.com/datalens-tech/datalens-cli/internal/rpchelp"
	"github.com/datalens-tech/datalens-cli/internal/schemacache"
	"github.com/spf13/cobra"
)

func (a *app) rpcCommand() *cobra.Command {
	var skipSchema bool
	command := &cobra.Command{
		Use:     "rpc ACTION [JSON|-|@FILE]",
		Short:   "Call a DataLens RPC action",
		Long:    "Call a DataLens RPC action by name and print its JSON response. Pass a JSON object after the action name, use - to read stdin, or use @FILE to read a file. Input from stdin or a file can be up to 16 MiB. Omit the arguments to send {}. The CLI checks the arguments against the selected installation's schema when available. Use datalens rpc ACTION --help for an argument summary or datalens schema show ACTION for the full JSON argument schema.",
		Example: "  datalens rpc getTenantDetails\n  " + rpchelp.ExampleCommands("getWorkbook", `{"workbookId":"your-workbook-id"}`, ""),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !cmd.Flags().Changed("args") && !cmd.Flags().Changed("no-validate") {
				return cmd.Help()
			}
			body, err := rpcArguments(cmd, args, false)
			if err != nil {
				return err
			}
			profile, err := a.selectedProfile()
			if err != nil {
				return err
			}
			return a.call(cmd, profile, args[0], body, !skipSchema)
		},
	}
	command.Flags().String("args", "{}", "Pass JSON, -, or @FILE as a flag instead of a positional argument")
	command.Flags().BoolVar(&skipSchema, "no-validate", false, "Skip the argument schema check for this request")
	return command
}

func rpcArguments(cmd *cobra.Command, args []string, helpOnly bool) (json.RawMessage, error) {
	var raw string
	switch len(args) {
	case 0:
		return nil, usageError("Specify an RPC action. See 'datalens rpc --help' for examples.")
	case 1:
		raw, _ = cmd.Flags().GetString("args")
	case 2:
		if cmd.Flags().Changed("args") {
			return nil, usageError("Pass the JSON object only once, either after the action name or with --args.")
		}
		raw = args[1]
	default:
		return nil, usageError("Pass the JSON object as a single argument. In bash or zsh, wrap it in single quotes.")
	}
	if raw != "-" && !strings.HasPrefix(raw, "@") {
		body := json.RawMessage(raw)
		return body, api.ValidateRPC(args[0], body)
	}
	if err := api.ValidateAction(args[0]); err != nil || helpOnly {
		return nil, err
	}
	input := cmd.InOrStdin()
	if path, fromFile := strings.CutPrefix(raw, "@"); fromFile {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, usageError("Pass @FILE with a readable regular file containing the JSON arguments.")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, usageError("Could not open the RPC arguments file.")
		}
		defer file.Close()
		input = file
	}
	body, err := readRPCInput(cmd.Context(), input)
	if err != nil {
		return nil, err
	}
	return body, api.ValidateRPC(args[0], body)
}

func readRPCInput(ctx context.Context, input io.Reader) (json.RawMessage, error) {
	const limit = 16 << 20
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(input, limit+1))
		done <- result{data, err}
	}()
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case read := <-done:
		if read.err != nil {
			return nil, usageError("Could not read the RPC arguments.")
		}
		if len(read.data) > limit {
			return nil, usageError("RPC arguments from stdin or a file must fit within 16 MiB.")
		}
		return read.data, nil
	}
}

func (a *app) schemaCommand() *cobra.Command {
	schema := &cobra.Command{Use: "schema", Short: "Inspect and update RPC argument schemas", Args: groupArgs, RunE: showHelp}
	schema.AddCommand(&cobra.Command{
		Use: "show ACTION", Short: "Print the full RPC argument schema as JSON", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := api.ValidateAction(args[0]); err != nil {
				return err
			}
			profile, err := a.selectedProfile()
			if err != nil {
				return err
			}
			catalog, err := profile.catalog()
			if err != nil {
				return err
			}
			data, err := catalog.Schema(args[0], profile.Installation)
			if err != nil {
				return err
			}
			return a.writer.APIResult(data)
		},
	})
	schema.AddCommand(&cobra.Command{
		Use: "update", Short: "Download the RPC schema for the selected profile", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := a.selectedProfile()
			if err != nil {
				return err
			}
			if err := schemacache.Update(cmd.Context(), p.dir, p.Profile, a.options.Transport); err != nil {
				return &output.Error{Code: "SCHEMA_UPDATE_FAILED", Message: fmt.Sprintf("Could not update the RPC schema for profile %s: %v", p.name, err), ExitCode: 1}
			}
			return a.writer.Result(map[string]string{"name": p.name}, fmt.Sprintf("Updated the RPC schema for profile %s.\n", p.name))
		},
	})
	return schema
}

func (a *app) tenantCommand() *cobra.Command {
	tenant := &cobra.Command{Use: "tenant", Short: "Inspect the current tenant", Args: groupArgs, RunE: showHelp}
	settings := &cobra.Command{Use: "settings", Short: "Inspect tenant settings", Args: groupArgs, RunE: showHelp}
	settings.AddCommand(&cobra.Command{
		Use: "show", Short: "Show Cloud tenant details and public settings", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, err := a.selectedProfile()
			if err != nil {
				return err
			}
			if profile.Installation != "cloud" {
				return &output.Error{
					Code:     "UNSUPPORTED_INSTALLATION",
					Message:  fmt.Sprintf("Tenant settings require a Cloud profile. The selected profile uses %s.", profile.Installation),
					ExitCode: 2,
				}
			}
			return a.call(cmd, profile, "getTenantDetails", json.RawMessage("{}"), true)
		},
	})
	tenant.AddCommand(settings)
	return tenant
}

func showHelp(cmd *cobra.Command, _ []string) error { return cmd.Help() }

func (a *app) showRPCActionHelp(cmd *cobra.Command, args []string) error {
	if _, err := rpcArguments(cmd, args, true); err != nil {
		return err
	}
	if cmd.Flags().Changed("profile") && a.profile == "" {
		return usageError("--profile requires a name")
	}
	profile, err := a.selectedProfile()
	if err != nil {
		return err
	}
	catalog, err := profile.catalog()
	if err != nil {
		return err
	}
	return catalog.Write(cmd.OutOrStdout(), args[0], profile.Installation, a.profile)
}

type profileContext struct {
	config.Profile
	name string
	dir  string
}

func (p profileContext) catalog() (*rpchelp.Catalog, error) {
	c, err := schemacache.Load(p.dir, p.Profile)
	if err != nil {
		return nil, &output.Error{Code: "SCHEMA_UNAVAILABLE", Message: fmt.Sprintf("Cannot load the RPC schema for profile %s. %v Run 'datalens schema update --profile=%s'.", p.name, err, p.name), ExitCode: 2}
	}
	return c, nil
}

func (a *app) selectedProfile() (profileContext, error) {
	store, err := a.store()
	if err != nil {
		return profileContext{}, err
	}
	doc, err := store.Load()
	if err != nil {
		return profileContext{}, err
	}
	name, profile, err := config.Resolve(doc, a.profile, a.options.LookupEnv("DATALENS_PROFILE"))
	return profileContext{Profile: profile, name: name, dir: store.Dir}, err
}

func (a *app) call(cmd *cobra.Command, profile profileContext, action string, args json.RawMessage, validate bool) error {
	if validate {
		catalog, err := profile.catalog()
		if err != nil {
			return err
		}
		if err := catalog.ValidateArgs(action, a.profile, args); err != nil {
			return err
		}
	}
	if profile.RequiresOrganization() && profile.OrganizationID == "" {
		profile.OrganizationID = a.options.LookupEnv("DATALENS_ORG_ID")
	}
	var explicitToken *string
	if cmd.Flags().Changed("auth-token") {
		explicitToken = &a.authToken
	}
	var helperStderr io.Writer
	if a.format == "text" {
		helperStderr = cmd.ErrOrStderr()
	}
	result, err := api.Call(cmd.Context(), a.options.Transport, profile.Profile, action, args, explicitToken, a.options.LookupEnv, helperStderr)
	if err != nil {
		return err
	}
	return a.writer.APIResult(result)
}
