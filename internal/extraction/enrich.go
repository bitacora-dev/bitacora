package extraction

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// builtinEnrichers lists the enricher names a rule's emit.enrich may
// reference — checked at rule-load time so a typo fails loudly instead of
// silently doing nothing.
var builtinEnrichers = map[string]bool{
	"cpu_from_context": true,
	"core_from_cpu":    true,
}

// enricherFunc mutates attrs in place, best-effort: it never fails the
// whole match just because enrichment couldn't add something (a process
// that's already gone by collection time, e.g. — the common case for a
// segfault, since the kernel kills it).
type enricherFunc func(attrs map[string]string)

// cpuFromContext implements ADR-0006's "cpu_from_context": correlates the
// event with the logical CPU it ran on, by reading /proc/<pid>/stat if
// the process still exists.
//
// ADR-0006 also mentions falling back to "el contexto del mensaje del
// kernel" (nearby journal entries) when the process is already gone —
// that needs access to log lines around this one, which a single-line
// regex match doesn't have. That's ADR-0011's black box territory
// (segfault↔CPU-topology correlation is its stated differentiator), not
// this task's scope. Flagged as a followup, not silently dropped.
func (e *Enrichers) cpuFromContext(attrs map[string]string) {
	pid := attrs["pid"]
	if pid == "" {
		return
	}

	raw, err := os.ReadFile(filepath.Join(e.ProcRoot, pid, "stat"))
	if err != nil {
		return // process already gone — expected for a segfault, not an error
	}

	// Fields after "comm)" start at field 3; field 39 overall (processor)
	// is index 36 in that zero-based remainder — same parsing shape as
	// internal/resourcebudget's /proc/pid/stat reader.
	s := string(raw)
	idx := strings.LastIndex(s, ")")
	if idx == -1 {
		return
	}
	fields := strings.Fields(s[idx+1:])
	const processorFieldIndex = 36 // field 39 overall, 0-based after comm)
	if len(fields) <= processorFieldIndex {
		return
	}
	if _, err := strconv.Atoi(fields[processorFieldIndex]); err == nil {
		attrs["cpu"] = fields[processorFieldIndex]
	}
}

// coreFromCPU implements ADR-0006's "core_from_cpu": maps the logical CPU
// number cpu_from_context found to its physical core ID, from cpu
// topology.
func (e *Enrichers) coreFromCPU(attrs map[string]string) {
	cpu := attrs["cpu"]
	if cpu == "" {
		return
	}

	path := filepath.Join(e.SysRoot, "devices", "system", "cpu", fmt.Sprintf("cpu%s", cpu), "topology", "core_id")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	coreID := strings.TrimSpace(string(raw))
	if _, err := strconv.Atoi(coreID); err == nil {
		attrs["core_id"] = coreID
	}
}

// Enrichers holds the filesystem roots the built-in enrichers read from —
// injectable so tests point at testdata/ instead of the real /proc, /sys.
type Enrichers struct {
	ProcRoot string
	SysRoot  string
}

// DefaultEnrichers reads the real /proc and /sys.
func DefaultEnrichers() Enrichers {
	return Enrichers{ProcRoot: "/proc", SysRoot: "/sys"}
}

func (e *Enrichers) funcs() map[string]enricherFunc {
	return map[string]enricherFunc{
		"cpu_from_context": e.cpuFromContext,
		"core_from_cpu":    e.coreFromCPU,
	}
}
