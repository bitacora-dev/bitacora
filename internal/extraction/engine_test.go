package extraction

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func loadKernelSegfaultRule(t *testing.T) *Rule {
	t.Helper()
	rules, err := LoadDir("rules")
	if err != nil {
		t.Fatalf("unexpected error loading rules: %v", err)
	}
	for _, r := range rules {
		if r.ID == "kernel-segfault" {
			return r
		}
	}
	t.Fatal("kernel-segfault rule not found in rules/")
	return nil
}

func testEnrichers() Enrichers {
	return Enrichers{ProcRoot: "testdata/proc", SysRoot: "testdata/sys"}
}

func TestEngine_MatchesKernelSegfaultAndEnriches(t *testing.T) {
	engine := NewEngine([]*Rule{loadKernelSegfaultRule(t)}, testEnrichers())

	ts := time.Date(2026, 8, 25, 1, 5, 12, 0, time.UTC)
	line := schema.LogLine{
		TS:     ts,
		HostID: "host-a",
		Source: "kernel",
		Message: "node[4242]: segfault at 0 ip 00007f2a12345678 sp 00007ffd87654321 " +
			"error 4 in libnode.so.115[7f2a12000000+abc000]",
	}

	event, err := engine.Process(context.Background(), line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected the segfault line to match and produce an event")
	}

	if event.Type != "kernel.segfault" {
		t.Fatalf("expected type kernel.segfault, got %q", event.Type)
	}
	if event.Severity != schema.SeverityError {
		t.Fatalf("expected severity error, got %q", event.Severity)
	}
	if event.HostID != "host-a" || !event.TS.Equal(ts) {
		t.Fatalf("expected host_id/ts carried over from the log line, got host_id=%q ts=%v", event.HostID, event.TS)
	}
	if event.Attrs["comm"] != "node" || event.Attrs["pid"] != "4242" {
		t.Fatalf("expected comm=node pid=4242 from the match, got %+v", event.Attrs)
	}
	if event.Subject.Kind != "process" || event.Subject.Name != "node" || event.Subject.PID != 4242 {
		t.Fatalf("expected subject process/node/4242, got %+v", event.Subject)
	}

	// pid 4242 has a testdata/proc fixture with processor field = 8.
	if event.Attrs["cpu"] != "8" {
		t.Fatalf("expected cpu_from_context to set cpu=8, got %+v", event.Attrs)
	}
	// testdata/sys's cpu8 topology fixture has core_id = 4.
	if event.Attrs["core_id"] != "4" {
		t.Fatalf("expected core_from_cpu to set core_id=4, got %+v", event.Attrs)
	}

	if event.Title != "segfault in node (cpu 8)" {
		t.Fatalf("expected rendered title 'segfault in node (cpu 8)', got %q", event.Title)
	}

	if err := event.Validate(); err != nil {
		t.Fatalf("expected the produced event to be a valid schema.Event, got %v", err)
	}
}

func TestEngine_DegradesGracefullyWhenProcessAlreadyGone(t *testing.T) {
	engine := NewEngine([]*Rule{loadKernelSegfaultRule(t)}, testEnrichers())

	line := schema.LogLine{
		TS: time.Now(), HostID: "host-a", Source: "kernel",
		Message: "node[9999]: segfault at 8 ip 00007f2a99999999 sp 00007ffd11112222 error 6 in libnode.so.115[7f2a12000000+abc000]",
	}

	event, err := engine.Process(context.Background(), line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected a match even without enrichment data")
	}
	if _, ok := event.Attrs["cpu"]; ok {
		t.Fatalf("expected no cpu attr when /proc/<pid> doesn't exist, got %+v", event.Attrs)
	}
}

func TestEngine_NonMatchingLineReturnsNilNotError(t *testing.T) {
	engine := NewEngine([]*Rule{loadKernelSegfaultRule(t)}, testEnrichers())

	line := schema.LogLine{TS: time.Now(), HostID: "host-a", Source: "kernel", Message: "CPU governor changed to performance"}
	event, err := engine.Process(context.Background(), line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Fatalf("expected no event for a non-matching line, got %+v", event)
	}
}

func TestEngine_SourceMismatchDoesNotMatch(t *testing.T) {
	engine := NewEngine([]*Rule{loadKernelSegfaultRule(t)}, testEnrichers())

	line := schema.LogLine{
		TS: time.Now(), HostID: "host-a", Source: "docker", // rule requires source: kernel
		Message: "node[4242]: segfault at 0 ip 00007f2a12345678 sp 00007ffd87654321 error 4 in libnode.so.115[7f2a12000000+abc000]",
	}
	event, err := engine.Process(context.Background(), line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Fatalf("expected the source mismatch to prevent a match, got %+v", event)
	}
}

func TestEngine_SameBinaryAndCPUShareFingerprint(t *testing.T) {
	engine := NewEngine([]*Rule{loadKernelSegfaultRule(t)}, testEnrichers())
	ctx := context.Background()

	// Two different pids, same comm — but only pid 4242 has a /proc
	// fixture, so give both a fixed cpu by using the enrichment-free path:
	// same comm, and skip cpu (both empty) still counts as "the same
	// recurrence" for fingerprinting purposes.
	line1 := schema.LogLine{TS: time.Now(), HostID: "host-a", Source: "kernel",
		Message: "node[1111]: segfault at 0 ip 00007f2a12345678 sp 00007ffd87654321 error 4 in libnode.so.115[x]"}
	line2 := schema.LogLine{TS: time.Now(), HostID: "host-a", Source: "kernel",
		Message: "node[2222]: segfault at 9 ip 00007fdeadbeef00 sp 00007ffd00000000 error 4 in libnode.so.115[x]"}

	e1, err := engine.Process(ctx, line1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e2, err := engine.Process(ctx, line2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if e1.Fingerprint != e2.Fingerprint {
		t.Fatalf("expected two segfaults from the same binary (same comm, no cpu resolved for either) to share a fingerprint, got %q vs %q", e1.Fingerprint, e2.Fingerprint)
	}
	if e1.Fingerprint == e2.ID {
		t.Fatal("fingerprint must not equal the event ID")
	}
}

func TestEngine_DifferentBinaryDifferentFingerprint(t *testing.T) {
	engine := NewEngine([]*Rule{loadKernelSegfaultRule(t)}, testEnrichers())
	ctx := context.Background()

	nodeLine := schema.LogLine{TS: time.Now(), HostID: "host-a", Source: "kernel",
		Message: "node[1111]: segfault at 0 ip 00007f2a12345678 sp 00007ffd87654321 error 4 in libnode.so.115[x]"}
	pythonLine := schema.LogLine{TS: time.Now(), HostID: "host-a", Source: "kernel",
		Message: "python3[3333]: segfault at 0 ip 00007f2a12345678 sp 00007ffd87654321 error 4 in libpython.so[x]"}

	nodeEvent, err := engine.Process(ctx, nodeLine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pyEvent, err := engine.Process(ctx, pythonLine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if nodeEvent.Fingerprint == pyEvent.Fingerprint {
		t.Fatal("expected segfaults from different binaries to have different fingerprints")
	}
}

// TestEngine_MatchesRealCorpus runs the engine against
// testdata/logs/kernel-segfault.log line by line — the corpus-driven test
// ADR-0006 asks each extraction rule to have.
func TestEngine_MatchesRealCorpus(t *testing.T) {
	engine := NewEngine([]*Rule{loadKernelSegfaultRule(t)}, testEnrichers())

	f, err := os.Open("testdata/logs/kernel-segfault.log")
	if err != nil {
		t.Fatalf("unexpected error opening corpus: %v", err)
	}
	defer f.Close()

	var matched, total int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		msg := strings.TrimSpace(scanner.Text())
		if msg == "" {
			continue
		}
		total++
		line := schema.LogLine{TS: time.Now(), HostID: "host-a", Source: "kernel", Message: msg}
		event, err := engine.Process(context.Background(), line)
		if err != nil {
			t.Fatalf("unexpected error processing corpus line %q: %v", msg, err)
		}
		if event != nil {
			matched++
			if event.Type != "kernel.segfault" {
				t.Fatalf("unexpected event type for line %q: %q", msg, event.Type)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("unexpected error reading corpus: %v", err)
	}

	if total == 0 {
		t.Fatal("corpus file is empty — nothing was exercised")
	}
	if matched == 0 || matched == total {
		t.Fatalf("expected the corpus to contain both matching and non-matching lines, matched %d of %d", matched, total)
	}
}
