// bitacora-run is the primary instrumentation path for anything periodic
// (ADR-0010): a small wrapper anteponed to any command —
//
//	bitacora-run --job rclone-aginsur-sync -- rclone sync /mnt/storage/aginsur remote:aginsur --use-json-log
//
// — that runs the command unchanged, captures its real exit code and (if
// it was killed) signal, extracts canonical stats from its output when a
// matching extractor exists, and hands the resulting Job off to the local
// agent or the spool. It always runs the wrapped command and always
// propagates its exit code, even if every instrumentation step around it
// fails: a backup that silently stops running because its monitoring
// wrapper broke would be worse than no monitoring at all.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bitacora-dev/bitacora/internal/execwrap"
	"github.com/bitacora-dev/bitacora/internal/jobwriter"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bitacora-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jobName := fs.String("job", "", "job name this run is recorded under (required)")
	timeout := fs.Duration("timeout", 0, "kill the command if it runs longer than this (0 = no timeout)")
	grace := fs.Duration("grace", 5*time.Second, "SIGTERM-to-SIGKILL grace period on timeout or interruption")
	socketPath := fs.String("socket", jobwriter.DefaultSocketPath, "agent Unix socket path")
	spoolDir := fs.String("spool-dir", jobwriter.DefaultSpoolDir, "spool directory used when the agent is unreachable")
	hostIDPath := fs.String("host-id-file", schema.DefaultHostIDPath, "path to the persisted host_id")
	peerHostID := fs.String("peer-host-id", "", "the other host involved in this job, if any (ADR-0010)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	argv := fs.Args()
	if *jobName == "" || len(argv) == 0 {
		fmt.Fprintln(stderr, "usage: bitacora-run --job NAME [flags] -- COMMAND [ARGS...]")
		fs.PrintDefaults()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := execwrap.Run(ctx, argv, execwrap.Options{
		Timeout:           *timeout,
		GracePeriod:       *grace,
		PassthroughStdout: stdout,
		PassthroughStderr: stderr,
	})
	if err != nil {
		// The command never ran at all (bad binary, permissions, ...) —
		// the one case where bitacora-run's own error IS the command's
		// failure, since there's no exit code to propagate instead.
		fmt.Fprintln(stderr, "bitacora-run:", err)
		return 1
	}

	instrument(stderr, *jobName, argv, *peerHostID, *hostIDPath, *socketPath, *spoolDir, result)

	return exitCodeFor(result)
}

// instrument builds and delivers the Job for a command that has already
// run to completion. Every step here is best-effort: a panic or error
// anywhere in here is logged and swallowed, never allowed to change the
// exit code run() already determined from the command itself
// (ADR-0010: "ante cualquier error interno, ejecuta el comando igualmente").
func instrument(stderr io.Writer, jobName string, argv []string, peerHostID, hostIDPath, socketPath, spoolDir string, result execwrap.Result) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(stderr, "bitacora-run: internal instrumentation error (command already ran, exit code unaffected):", r)
		}
	}()

	hostID, err := schema.LoadOrCreateHostID(hostIDPath)
	if err != nil {
		fmt.Fprintln(stderr, "bitacora-run: loading host_id:", err)
	}

	job, extractErrs := buildJob(jobName, hostID, argv, detectTrigger(), result)
	job.PeerHostID = peerHostID
	for _, e := range extractErrs {
		fmt.Fprintln(stderr, "bitacora-run:", e)
	}

	w := jobwriter.New(socketPath, spoolDir)
	if _, err := w.Write(job); err != nil {
		fmt.Fprintln(stderr, "bitacora-run: recording job:", err)
	}
}
