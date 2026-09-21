package journald

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

const maxSegfaultCommLength = 64

// parseSegfaultMessage accepts the stable kernel shape before its variable
// address and library details. Process names are untrusted journal input, so
// only a short ASCII-safe subset is admitted into persisted event attributes.
func parseSegfaultMessage(message string) (comm string, pid int, ok bool) {
	message = strings.TrimSpace(message)
	if strings.HasPrefix(message, "[") {
		if end := strings.Index(message, "]"); end >= 0 {
			message = strings.TrimSpace(message[end+1:])
		}
	}

	nameEnd := strings.Index(message, "[")
	if nameEnd <= 0 {
		return "", 0, false
	}
	comm = message[:nameEnd]
	if len(comm) > maxSegfaultCommLength || !safeComm(comm) {
		return "", 0, false
	}

	pidEnd := strings.Index(message[nameEnd:], "]: segfault")
	if pidEnd < 0 {
		return "", 0, false
	}
	pidRaw := message[nameEnd+1 : nameEnd+pidEnd]
	pid64, err := strconv.ParseInt(pidRaw, 10, 0)
	if err != nil || pid64 <= 0 {
		return "", 0, false
	}
	return comm, int(pid64), true
}

func safeComm(comm string) bool {
	for _, r := range comm {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '.' && r != '_' && r != '-' && r != '+' {
			return false
		}
	}
	return comm != ""
}

func (c *Collector) observeSegfault(entry Entry) *schema.Event {
	if c.faultTracker == nil || entry.Fields["_TRANSPORT"] != "kernel" {
		return nil
	}
	comm, pid, ok := parseSegfaultMessage(entry.Fields["MESSAGE"])
	if !ok {
		return nil
	}
	cpu, ok := readProcessor(c.procRoot, pid)
	if !ok {
		return nil
	}
	return c.faultTracker.Observe(c.hostID, comm, cpu, c.entryToLogLine(entry).TS)
}

// readProcessor reads field 39 (processor) from /proc/<pid>/stat. The
// parenthesised command can contain spaces, so split only after its final ')'.
func readProcessor(procRoot string, pid int) (int, bool) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, false
	}
	closeParen := strings.LastIndex(string(raw), ")")
	if closeParen < 0 {
		return 0, false
	}
	fields := strings.Fields(string(raw)[closeParen+1:])
	const processorAfterCommIndex = 36 // stat field 39, after pid and comm.
	if len(fields) <= processorAfterCommIndex {
		return 0, false
	}
	cpu, err := strconv.Atoi(fields[processorAfterCommIndex])
	if err != nil || cpu < 0 {
		return 0, false
	}
	return cpu, true
}
