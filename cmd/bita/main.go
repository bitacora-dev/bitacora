// bita is Bitácora's administration CLI. It never shells out to the system
// (ADR-0012) — everything it reports comes from direct filesystem and
// os/user lookups.
package main

import (
	"fmt"
	"os"

	"github.com/bitacora-dev/bitacora/internal/doctor"
	"github.com/bitacora-dev/bitacora/internal/logstore"
)

// DefaultLogsDir is where the log block store lives in production
// (ADR-0003).
const DefaultLogsDir = "/var/lib/bitacora/logs"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("bita: scaffold only, most subcommands not yet implemented")
		fmt.Println("usage: bita doctor")
		fmt.Println("       bita logs verify [dir]")
		return
	}

	switch os.Args[1] {
	case "doctor":
		runDoctor()
	case "logs":
		runLogs(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "bita: unknown subcommand %q\n", os.Args[1])
		os.Exit(1)
	}
}

func runLogs(args []string) {
	if len(args) < 1 || args[0] != "verify" {
		fmt.Fprintln(os.Stderr, "usage: bita logs verify [dir]")
		os.Exit(1)
	}

	dir := DefaultLogsDir
	if len(args) > 1 {
		dir = args[1]
	}

	result, err := logstore.ScanIndex(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bita logs verify: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%d blocks reconstructed from %s\n", len(result.Blocks), dir)
	fmt.Printf("  raw:        %d bytes\n", result.TotalRawBytes)
	fmt.Printf("  compressed: %d bytes\n", result.TotalCompBytes)

	problems := len(result.OrphanPayloads) + len(result.OrphanMeta) + len(result.Corrupt)
	if problems == 0 {
		fmt.Println("no orphans, no corrupt metadata")
		return
	}

	for _, p := range result.OrphanPayloads {
		fmt.Printf("[FAIL] orphan payload (no .meta.json): %s\n", p)
	}
	for _, p := range result.OrphanMeta {
		fmt.Printf("[FAIL] orphan metadata (no .zst payload): %s\n", p)
	}
	for p, reason := range result.Corrupt {
		fmt.Printf("[FAIL] corrupt metadata: %s (%s)\n", p, reason)
	}
	os.Exit(1)
}

func runDoctor() {
	checks := doctor.Run(doctor.DefaultConfig)

	failed := 0
	for _, c := range checks {
		status := "OK"
		if !c.OK {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %-24s %s\n", status, c.Name, c.Detail)
	}

	if failed > 0 {
		os.Exit(1)
	}
}
