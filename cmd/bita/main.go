// bita is Bitácora's administration CLI. It never shells out to the system
// (ADR-0012) — everything it reports comes from direct filesystem and
// os/user lookups.
package main

import (
	"fmt"
	"os"

	"github.com/bitacora-dev/bitacora/internal/doctor"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("bita: scaffold only, most subcommands not yet implemented")
		fmt.Println("usage: bita doctor")
		return
	}

	switch os.Args[1] {
	case "doctor":
		runDoctor()
	default:
		fmt.Fprintf(os.Stderr, "bita: unknown subcommand %q\n", os.Args[1])
		os.Exit(1)
	}
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
