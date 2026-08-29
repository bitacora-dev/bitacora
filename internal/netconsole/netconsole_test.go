package netconsole

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

type recordingReceiver struct {
	mu    sync.Mutex
	lines []schema.LogLine
}

func (r *recordingReceiver) ReceiveLogLine(ctx context.Context, line schema.LogLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	return nil
}

func (r *recordingReceiver) waitFor(t *testing.T, n int) []schema.LogLine {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.lines)
		lines := append([]schema.LogLine(nil), r.lines...)
		r.mu.Unlock()
		if got >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d received line(s), got %d", n, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func startServer(t *testing.T, srv *Server) (addr *net.UDPAddr, stop func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	addr = conn.LocalAddr().(*net.UDPAddr)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, conn); close(done) }()

	return addr, func() { cancel(); <-done }
}

func sendUDP(t *testing.T, addr *net.UDPAddr, payload string) {
	t.Helper()
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServer_DecodesBasicFormat(t *testing.T) {
	receiver := &recordingReceiver{}
	addr, stop := startServer(t, &Server{Receiver: receiver})
	defer stop()

	sendUDP(t, addr, "<3>md/raid1:md0: not clean -- starting background reconstruction\n")

	lines := receiver.waitFor(t, 1)
	if lines[0].Message != "md/raid1:md0: not clean -- starting background reconstruction" {
		t.Fatalf("unexpected message: %q", lines[0].Message)
	}
	if lines[0].Level != "error" {
		t.Fatalf("expected level error (priority 3), got %q", lines[0].Level)
	}
	if lines[0].Source != "kernel_remote" {
		t.Fatalf("expected source kernel_remote, got %q", lines[0].Source)
	}
}

func TestServer_DecodesExtendedFormat(t *testing.T) {
	receiver := &recordingReceiver{}
	addr, stop := startServer(t, &Server{Receiver: receiver})
	defer stop()

	// facility 0 (kern) * 8 + level 2 (crit) = 2
	sendUDP(t, addr, "2,12345,987654321,-;Kernel panic - not syncing: Fatal exception\n")

	lines := receiver.waitFor(t, 1)
	if lines[0].Message != "Kernel panic - not syncing: Fatal exception" {
		t.Fatalf("unexpected message: %q", lines[0].Message)
	}
	if lines[0].Level != "critical" {
		t.Fatalf("expected level critical, got %q", lines[0].Level)
	}
}

func TestServer_PlainMessageWithNoLevelPrefixStillDecodes(t *testing.T) {
	receiver := &recordingReceiver{}
	addr, stop := startServer(t, &Server{Receiver: receiver})
	defer stop()

	sendUDP(t, addr, "a line with no bracket prefix at all\n")

	lines := receiver.waitFor(t, 1)
	if lines[0].Message != "a line with no bracket prefix at all" {
		t.Fatalf("unexpected message: %q", lines[0].Message)
	}
	if lines[0].Level != "" {
		t.Fatalf("expected no level for an unprefixed line, got %q", lines[0].Level)
	}
}

func TestServer_EmptyPacketIsDroppedNotDelivered(t *testing.T) {
	receiver := &recordingReceiver{}
	addr, stop := startServer(t, &Server{Receiver: receiver})
	defer stop()

	sendUDP(t, addr, "")
	sendUDP(t, addr, "<6>a real line after the empty one\n")

	lines := receiver.waitFor(t, 1)
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 delivered line, got %d", len(lines))
	}
}

func TestServer_FallsBackToSourceIPWithoutAResolver(t *testing.T) {
	receiver := &recordingReceiver{}
	addr, stop := startServer(t, &Server{Receiver: receiver}) // no Resolver set
	defer stop()

	sendUDP(t, addr, "<6>hello\n")

	lines := receiver.waitFor(t, 1)
	if lines[0].HostID != "127.0.0.1" {
		t.Fatalf("expected HostID to fall back to the source IP, got %q", lines[0].HostID)
	}
}

func TestServer_UsesResolverWhenItReturnsAHostID(t *testing.T) {
	receiver := &recordingReceiver{}
	srv := &Server{
		Receiver: receiver,
		Resolver: func(addr *net.UDPAddr) string { return "host-icloudserver" },
	}
	addr, stop := startServer(t, srv)
	defer stop()

	sendUDP(t, addr, "<6>hello\n")

	lines := receiver.waitFor(t, 1)
	if lines[0].HostID != "host-icloudserver" {
		t.Fatalf("expected the resolver's host_id, got %q", lines[0].HostID)
	}
}
