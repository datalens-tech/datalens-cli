package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/datalens-tech/datalens-cli/internal/cli"
	"github.com/datalens-tech/datalens-cli/internal/output"
	"golang.org/x/term"
)

func main() {
	signal.Ignore(syscall.SIGPIPE)
	os.Exit(run())
}

func run() int {
	if len(os.Args) == 2 && os.Args[1] == refreshCommand {
		refreshUpdateNotice()
		return 0
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		select {
		case received := <-signals:
			signal.Stop(signals)
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			}
			cancel(&output.Error{Code: "INTERRUPTED", Message: "command interrupted", ExitCode: code})
		case <-ctx.Done():
		}
	}()
	return cli.Execute(ctx, os.Args[1:], cli.Options{
		Out:                 processWriter{os.Stdout},
		Err:                 processWriter{os.Stderr},
		OutputsAreTerminals: term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd())),
		NotifyUpdate:        notifyUpdate,
	})
}
