package cli

import (
	"encoding/json"

	"github.com/datalens-tech/datalens-cli/internal/api"
	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/rpchelp"
	"github.com/spf13/cobra"
)

func (a *app) rpcCommand() *cobra.Command {
	var argsJSON string
	command := &cobra.Command{
		Use:   "rpc ACTION",
		Short: "Call a DataLens RPC action",
		Long:  "Call a DataLens RPC action by name and print its JSON response. Pass action parameters as a JSON object with --args, or omit --args to send {}. Use datalens rpc ACTION --help to look up an action's arguments.",
		Example: `  datalens rpc getTenantDetails
  datalens rpc getWorkbook --args '{"workbookId":"your-workbook-id"}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !cmd.Flags().Changed("args") {
				return cmd.Help()
			}
			if err := exactArgs(1)(cmd, args); err != nil {
				return err
			}
			body := json.RawMessage(argsJSON)
			if err := api.ValidateRPC(args[0], body); err != nil {
				return err
			}
			return a.call(cmd, args[0], body)
		},
	}
	command.Flags().StringVar(&argsJSON, "args", "{}", "RPC arguments as a JSON object")
	return command
}

func (a *app) tenantCommand() *cobra.Command {
	tenant := &cobra.Command{Use: "tenant", Short: "Inspect the current tenant", Args: groupArgs, RunE: showHelp}
	settings := &cobra.Command{Use: "settings", Short: "Inspect tenant settings", Args: groupArgs, RunE: showHelp}
	settings.AddCommand(&cobra.Command{
		Use: "show", Short: "Show tenant details and public settings", Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.call(cmd, "getTenantDetails", json.RawMessage("{}"))
		},
	})
	tenant.AddCommand(settings)
	return tenant
}

func showHelp(cmd *cobra.Command, _ []string) error { return cmd.Help() }

func showRPCActionHelp(cmd *cobra.Command, args []string) error {
	if err := exactArgs(1)(cmd, args); err != nil {
		return err
	}
	if err := api.ValidateAction(args[0]); err != nil {
		return err
	}
	return rpchelp.Write(cmd.OutOrStdout(), args[0])
}

func (a *app) call(cmd *cobra.Command, action string, args json.RawMessage) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	doc, err := store.Load()
	if err != nil {
		return err
	}
	_, profile, err := config.Resolve(doc, a.profile, a.options.LookupEnv("DATALENS_PROFILE"))
	if err != nil {
		return err
	}
	if profile.Installation == "cloud" && profile.OrganizationID == "" {
		profile.OrganizationID = a.options.LookupEnv("DATALENS_ORG_ID")
	}
	var explicitToken *string
	if cmd.Flags().Changed("auth-token") {
		explicitToken = &a.authToken
	}
	token, err := api.ResolveToken(explicitToken, a.options.LookupEnv)
	if err != nil {
		return err
	}
	result, err := api.Call(cmd.Context(), a.options.Transport, profile, token, action, args)
	if err != nil {
		return err
	}
	return a.writer.APIResult(result)
}
