package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spatialwalk/open-platform-cli/internal/avtkitcli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := avtkitcli.Run(ctx, os.Args[1:], avtkitcli.Streams{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err == nil {
		return
	}

	if message := err.Error(); message != "" {
		fmt.Fprintln(os.Stderr, message)
	}
	os.Exit(avtkitcli.ExitCode(err))
}
