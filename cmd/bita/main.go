// bita is Bitácora's administration CLI. It never shells out to the system
// (ADR-0012) — everything it reports comes from direct filesystem and
// os/user lookups.
package main

import (
	"fmt"
	"os"

	"github.com/bitacora-dev/bitacora/internal/blackbox"
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
		fmt.Println("       bita blackbox dump <fichero>")
		return
	}

	switch os.Args[1] {
	case "doctor":
		runDoctor()
	case "logs":
		runLogs(os.Args[2:])
	case "blackbox":
		runBlackbox(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "bita: unknown subcommand %q\n", os.Args[1])
		os.Exit(1)
	}
}

// runBlackbox implements ADR-0011's own mandatory tooling requirement:
// "el formato del fichero de caja negra debe ser legible sin el agente:
// bita blackbox dump <fichero> debe funcionar sobre un fichero copiado
// desde otra máquina." blackbox.Dump only reads the file — no mmap, no
// agent, no assumption it's even running on the machine that wrote it.
func runBlackbox(args []string) {
	if len(args) < 2 || args[0] != "dump" {
		fmt.Fprintln(os.Stderr, "usage: bita blackbox dump <fichero>")
		os.Exit(1)
	}

	samples, err := blackbox.Dump(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bita blackbox dump: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(formatBlackboxSamples(samples))
}

func formatBlackboxSamples(samples []blackbox.Sample) string {
	out := fmt.Sprintf("%d sample(s)\n", len(samples))
	for _, s := range samples {
		out += fmt.Sprintf(
			"t=%d cpus=%d load1=%.2f mem_avail_kb=%d procs_blocked_d=%d psi_cpu_some10=%.2f\n",
			s.TimestampUnixMilli, s.NumCPUs, s.LoadAvg1, s.MemAvailableKB, s.ProcsBlockedD, s.PSICPUSome10,
		)
	}
	return out
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
