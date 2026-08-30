// Package netconsole implements the hub-side receiver for the kernel's
// own netconsole (ADR-0011): a UDP stream of printk lines sent live to
// another machine, so a host that hard-hangs before it can write anything
// to its own disk still gets its last kernel messages somewhere safe.
package netconsole

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// DefaultPort is netconsole's conventional UDP port (6666 is the kernel
// documentation's own example and what most guides use); nothing in the
// protocol requires it.
const DefaultPort = 6666

// Receiver is handed every LogLine a Server decodes. The hub wires this
// to real storage — same shape as transport.BatchReceiver and
// job.Receiver, so netconsole doesn't need to know what "storage" means.
type Receiver interface {
	ReceiveLogLine(ctx context.Context, line schema.LogLine) error
}

// HostIDResolver maps a UDP source address to a host_id. When it returns
// "" (including when Resolver itself is nil), the source IP is used
// directly as HostID — see this package's README for why a general
// IP-to-host_id registry doesn't exist yet.
type HostIDResolver func(addr *net.UDPAddr) string

// Server listens for netconsole UDP packets and decodes each into a
// schema.LogLine before handing it to Receiver.
type Server struct {
	Receiver Receiver
	Resolver HostIDResolver
}

// Serve reads from conn until ctx is done or conn is closed. One
// malformed packet is dropped, not fatal — the whole point of netconsole
// is capturing whatever made it out before a hang, and that can include
// truncated or out-of-order fragments.
func (s *Server) Serve(ctx context.Context, conn *net.UDPConn) error {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 65536) // a UDP datagram's own maximum size
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("reading netconsole packet: %w", err)
		}

		line, ok := s.decode(buf[:n], addr, time.Now().UTC())
		if !ok {
			continue
		}
		_ = s.Receiver.ReceiveLogLine(ctx, line)
	}
}

func (s *Server) decode(packet []byte, addr *net.UDPAddr, now time.Time) (schema.LogLine, bool) {
	msg, level := parseNetconsoleMessage(packet)
	if msg == "" {
		return schema.LogLine{}, false
	}

	hostID := ""
	if s.Resolver != nil {
		hostID = s.Resolver(addr)
	}
	if hostID == "" {
		hostID = addr.IP.String()
	}

	return schema.LogLine{
		TS:      now,
		HostID:  hostID,
		Source:  "kernel_remote",
		Level:   level,
		Message: msg,
	}, true
}

// extendedFormat matches netconsole's extended packet layout
// (Documentation/networking/netconsole.rst): "<PRI>,<SEQ>,<TS>,<FLAGS>;<MSG>",
// where PRI is facility*8+level (syslog priority). Level is PRI mod 8,
// same convention journald's own PRIORITY field uses (priorityToLevel in
// internal/collector/journald).
var extendedFormat = regexp.MustCompile(`^(\d+),(\d+),(\d+),([^;]*);(.*)$`)

// basicFormat matches netconsole's basic packet layout: an optional
// "<level>" prefix (the same bracket syntax printk itself uses) followed
// by the message, no sequencing metadata at all.
var basicFormat = regexp.MustCompile(`^<(\d)>(.*)$`)

func parseNetconsoleMessage(packet []byte) (message, level string) {
	text := strings.TrimRight(string(packet), "\n\x00")
	if text == "" {
		return "", ""
	}

	if m := extendedFormat.FindStringSubmatch(text); m != nil {
		pri, err := strconv.Atoi(m[1])
		msg := strings.TrimSpace(m[5])
		if err == nil && msg != "" {
			return msg, syslogLevel(pri % 8)
		}
	}

	if m := basicFormat.FindStringSubmatch(text); m != nil {
		if lvl, err := strconv.Atoi(m[1]); err == nil {
			msg := strings.TrimSpace(m[2])
			if msg != "" {
				return msg, syslogLevel(lvl)
			}
		}
	}

	return text, ""
}

var syslogLevels = map[int]string{
	0: "emerg", 1: "alert", 2: "critical", 3: "error",
	4: "warning", 5: "notice", 6: "info", 7: "debug",
}

func syslogLevel(n int) string {
	if l, ok := syslogLevels[n]; ok {
		return l
	}
	return ""
}
