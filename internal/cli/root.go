package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/buildinfo"
	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
	"github.com/datalens-tech/datalens-cli/internal/updatenotice"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type Options struct {
	In                  io.Reader
	Out                 io.Writer
	Err                 io.Writer
	LookupEnv           func(string) string
	UserConfigDir       func() (string, error)
	IsTerminal          func(io.Reader) bool
	OutputsAreTerminals bool
	NotifyUpdate        func(current string, out io.Writer)
	Build               buildinfo.Info
	Transport           http.RoundTripper
}

type app struct {
	options        Options
	profile        string
	authToken      string
	format         string
	timeout        time.Duration
	nonInteractive bool
	debug          bool
	writer         output.Writer
}

func Execute(ctx context.Context, args []string, options Options) int {
	if options.In == nil {
		options.In = os.Stdin
	}
	if options.Out == nil {
		options.Out = os.Stdout
	}
	if options.Err == nil {
		options.Err = os.Stderr
	}
	if options.LookupEnv == nil {
		options.LookupEnv = os.Getenv
	}
	if options.UserConfigDir == nil {
		options.UserConfigDir = defaultConfigDir
	}
	if options.IsTerminal == nil {
		options.IsTerminal = func(in io.Reader) bool {
			file, ok := in.(*os.File)
			return ok && term.IsTerminal(int(file.Fd()))
		}
	}
	if options.Build.Version == "" {
		options.Build = buildinfo.Current()
	}
	a := &app{options: options}
	root := a.root()
	root.SetArgs(args)
	cmd, flagArgs, findErr := root.Find(args)
	commandTimeout := a.timeout
	hasTimeout := readOutputFlags(cmd, flagArgs)
	if a.timeout > 0 {
		commandTimeout = a.timeout
	}
	isTerminal := options.IsTerminal(options.In)
	interactiveDelete := cmd.CommandPath() == "datalens profiles delete" &&
		isTerminal && !a.nonInteractive && a.format == "text" && cmd.Flag("confirm").Value.String() == ""
	commandContext := ctx
	if hasTimeout || (cmd.Name() != "setup" && !interactiveDelete) {
		var cancel context.CancelFunc
		commandContext, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
	}
	stdout := &commandStream{ctx: ctx, dest: options.Out}
	stderr := &commandStream{ctx: commandContext, dest: options.Err}
	a.options.Err = stderr
	root.SetIn(options.In)
	root.SetOut(stdout)
	root.SetErr(stderr)
	a.writer = output.Writer{Out: stdout, Err: &commandStream{ctx: ctx, dest: options.Err}, Format: "text"}
	if a.format == "json" {
		a.writer.Format = "json"
	}
	if a.debug {
		a.writer.Diagnostics = &output.Diagnostics{Version: options.Build.Version, Operation: cmd.CommandPath(), NonInteractive: a.nonInteractive || !isTerminal}
	}
	var helpErr error
	var shownHelp bool
	help := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		shownHelp = true
		if cmd.HasSubCommands() || cmd.Name() == "help" {
			if helpErr = cmd.ValidateArgs(cmd.Flags().Args()); helpErr != nil {
				return
			}
		}
		if cmd.Name() == "rpc" && cmd.Flags().NArg() > 0 {
			if helpErr = a.showRPCActionHelp(cmd, cmd.Flags().Args()); helpErr != nil {
				return
			}
		} else {
			help(cmd, args)
		}
		if stdout.err == nil && stderr.err == nil {
			stderr.err = a.writer.WriteDiagnostics()
		}
	})
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("profile") && a.profile == "" {
			return usageError("--profile requires a name")
		}
		if a.format != "text" && a.format != "json" {
			return usageError("format must be text or json")
		}
		if a.timeout <= 0 {
			return usageError("timeout must be greater than zero")
		}
		a.nonInteractive = a.nonInteractive || !isTerminal
		return context.Cause(cmd.Context())
	}
	err := context.Cause(commandContext)
	if err == nil {
		if findErr != nil {
			err = usageError("unknown command; see datalens help")
		} else {
			cmd, err = root.ExecuteContextC(commandContext)
		}
	}
	if err == nil {
		err = errors.Join(helpErr, stdout.err, stderr.err)
	}
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	} else if closedOutputPipe(stdout.err) {
		return 0
	}
	if err == nil {
		if !shownHelp && !cmd.Hidden && isTerminal && a.shouldNotifyUpdate() {
			options.NotifyUpdate(options.Build.Version, stderr)
		}
		return 0
	}
	if cause := context.Cause(commandContext); cause != nil {
		err = cause
		var stop context.CancelFunc
		// Allow the final error to reach stderr even after the command timed out.
		stderr.ctx, stop = context.WithTimeout(context.WithoutCancel(commandContext), 100*time.Millisecond)
		defer stop()
	}
	a.writer.Err = stderr
	failure := classifyFailure(err)
	if a.writer.Failure(failure) != nil {
		if cause := context.Cause(commandContext); cause != nil {
			return classifyFailure(cause).ExitCode
		}
		return 1
	}
	return failure.ExitCode
}

func (a *app) shouldNotifyUpdate() bool {
	options := a.options
	if options.NotifyUpdate == nil || !options.OutputsAreTerminals || a.nonInteractive || a.format != "text" {
		return false
	}
	ci := options.LookupEnv("CI")
	return (ci == "" || ci == "0" || strings.EqualFold(ci, "false")) &&
		options.LookupEnv("DATALENS_NO_UPDATE_CHECK") != "1" && updatenotice.ValidVersion(options.Build.Version)
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use: "datalens", Short: "Work with DataLens from the command line",
		Example:       "  datalens setup\n  datalens profiles create work --installation-type=cloud",
		SilenceErrors: true, SilenceUsage: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Args:              groupArgs,
		RunE:              showHelp,
	}
	flags := root.PersistentFlags()
	flags.StringVar(&a.profile, "profile", "", "Select a profile for RPC, schemas, tenant settings, or profiles list")
	flags.StringVar(&a.authToken, "auth-token", "", "Use this token for the current API request")
	flags.StringVar(&a.format, "format", "text", "Output format: text or json")
	flags.DurationVar(&a.timeout, "timeout", 30*time.Second, "Command timeout (interactive prompts wait unless explicitly set)")
	flags.BoolVar(&a.nonInteractive, "non-interactive", false, "Disable interactive prompts")
	flags.BoolVar(&a.debug, "debug", false, "Include safe diagnostic metadata")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, _ error) error {
		return usageError("invalid flag or value; see " + cmd.CommandPath() + " --help")
	})
	root.AddCommand(a.setupCommand(), a.versionCommand(), a.profilesCommand(), a.rpcCommand(), a.schemaCommand(), a.tenantCommand(), a.cowCommand())
	root.InitDefaultHelpCmd()
	help, _, _ := root.Find([]string{"help"})
	help.Args = func(cmd *cobra.Command, args []string) error {
		if _, tail, err := cmd.Root().Find(args); err != nil || len(tail) != 0 {
			return usageError("unknown help topic; see datalens help")
		}
		return nil
	}
	initHelpFlags(root)
	return root
}

// Find needs to know that -h takes no value when it precedes a subcommand.
func initHelpFlags(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	for _, child := range cmd.Commands() {
		initHelpFlags(child)
	}
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Show CLI version, API version, and build information", Args: exactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			info := a.options.Build
			return a.writer.Result(info, fmt.Sprintf("datalens %s (%s, %s/%s, %s, API v%s)\n", info.Version, info.Revision, info.OS, info.Arch, info.GoVersion, info.APIVersion))
		},
	}
}

func defaultConfigDir() (string, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return os.UserConfigDir()
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

func (a *app) store() (config.Store, error) {
	dir := a.options.LookupEnv("DATALENS_CONFIG_DIR")
	if dir == "" {
		base, err := a.options.UserConfigDir()
		if err != nil || base == "" {
			return config.Store{}, &output.Error{Code: "CONFIG_DIRECTORY_UNAVAILABLE", Message: "set DATALENS_CONFIG_DIR to a writable configuration directory", ExitCode: 2}
		}
		dir = filepath.Join(base, "datalens")
	}
	return config.Store{Dir: dir}, nil
}

func classifyFailure(err error) *output.Error {
	var failure *output.Error
	var configError *config.Error
	switch {
	case errors.As(err, &failure):
	case errors.As(err, &configError):
		failure = &output.Error{Code: configError.Code, Message: configError.Message, ExitCode: 2}
		if configError.Code == "CONFIG_IO" {
			failure.ExitCode = 1
		}
	case errors.Is(err, context.DeadlineExceeded):
		failure = &output.Error{Code: "TIMEOUT", Message: "command timed out", ExitCode: 1}
	case errors.Is(err, context.Canceled):
		failure = &output.Error{Code: "INTERRUPTED", Message: "command interrupted", ExitCode: 130}
	default:
		failure = &output.Error{Code: "COMMAND_FAILED", Message: "command failed", ExitCode: 1}
	}
	return failure
}

func exactArgs(count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(count)(cmd, args); err != nil {
			return usageError(err.Error())
		}
		return nil
	}
}

func groupArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return usageError("unknown command; see " + cmd.CommandPath() + " --help")
	}
	return nil
}

func usageError(message string) *output.Error {
	return &output.Error{Code: "INVALID_USAGE", Message: message, ExitCode: 2}
}

// Cobra can reject an earlier flag before it reaches the flags controlling the error report.
func readOutputFlags(cmd *cobra.Command, args []string) (hasTimeout bool) {
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		if arg == "--" {
			break
		}
		name, ok := strings.CutPrefix(arg, "--")
		if !ok {
			continue
		}
		name, value, assigned := strings.Cut(name, "=")
		flag := cmd.Flag(name)
		if flag == nil {
			continue
		}
		if !assigned {
			value = flag.NoOptDefVal
			if value == "" {
				if len(args) == 0 {
					break
				}
				value, args = args[0], args[1:]
			}
		}
		switch name {
		case "timeout":
			hasTimeout = true
			_ = flag.Value.Set(value)
		case "format", "debug", "non-interactive", "confirm":
			_ = flag.Value.Set(value)
		}
	}
	return hasTimeout
}
