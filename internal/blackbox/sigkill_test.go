package blackbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// harnessEnvVar, when set, makes this test binary behave as a standalone
// recorder instead of running the test suite — the standard Go idiom for
// a test that needs a real, killable child process, self-re-exec'd via
// os.Args[0] (used the same way in cmd/bitacora-run's own SIGKILL test).
const harnessEnvVar = "BLACKBOX_SIGKILL_HARNESS"

// TestMain intercepts the harness case before the normal test runner
// takes over.
func TestMain(m *testing.M) {
	if path := os.Getenv(harnessEnvVar); path != "" {
		runHarness(path)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runHarness opens a Recorder at path and records+syncs continuously,
// exactly like a real agent would, until something kills it.
func runHarness(path string) {
	rec, err := Open(path, 60)
	if err != nil {
		os.Exit(1)
	}
	defer rec.Close()

	var i int64
	for {
		rec.Record(sampleAt(i * 1000))
		i++
		if i%5 == 0 {
			_ = rec.Sync()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRecorder_SurvivesSIGKILL is ADR-0011's own mandatory test: "inyectar
// SIGKILL -9 al agente y verificar que el fichero mapeado contiene datos
// coherentes hasta el último volcado." A separate real process records
// into a real mmap'd file and gets SIGKILL'd mid-flight — no mocks, no
// simulated crash — and this process, which never touched that memory,
// reads the file back afterward.
func TestRecorder_SurvivesSIGKILL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blackbox.dat")

	cmd := exec.Command(os.Args[0], "-test.run=^TestRecorder_SurvivesSIGKILL$")
	cmd.Env = append(os.Environ(), harnessEnvVar+"="+path)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("unexpected error starting harness: %v", err)
	}

	// Let it record and sync several times before killing it mid-flight.
	time.Sleep(300 * time.Millisecond)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("unexpected error killing harness: %v", err)
	}
	_ = cmd.Wait() // exits non-zero/signaled — expected, not asserted here

	samples, err := Dump(path)
	if err != nil {
		t.Fatalf("blackbox file is unreadable after SIGKILL: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("expected at least one synced sample to have survived the SIGKILL")
	}

	// Coherent: every synced record decoded cleanly (Dump already proved
	// that) and timestamps are strictly increasing by the harness's own
	// 1000 ms step — no torn, no reordered, no partially-written record.
	for i := 1; i < len(samples); i++ {
		if samples[i].TimestampUnixMilli != samples[i-1].TimestampUnixMilli+1000 {
			t.Fatalf("non-contiguous timestamps at %d: %d then %d", i, samples[i-1].TimestampUnixMilli, samples[i].TimestampUnixMilli)
		}
	}
}
