package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bitacora-run: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: bitacora-run --job NAME [--trigger T] [--timeout DURATION] -- CMD [ARGS...]")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result := Execute(ctx, opts, os.Stdout, os.Stderr)
	os.Exit(result.ExitCode)
}
