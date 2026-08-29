package job

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// DefaultSocketPath is where the agent listens for local Job deliveries
// (ADR-0010: "Escribe el Job al agente local (socket Unix)").
const DefaultSocketPath = "/run/bitacora/agent.sock"

// Deliver sends job to the agent listening on socketPath: one JSON object
// per line, and one line of response ("ok" or "error: ...") read back
// before the connection is closed. It fails fast — dialing a socket that
// doesn't exist, or one nothing is listening on, returns immediately rather
// than blocking bitacora-run.
func Deliver(ctx context.Context, socketPath string, j Job) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dialing agent socket %s: %w", socketPath, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	encoded, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("marshaling job %s: %w", j.ID, err)
	}
	if _, err := conn.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("writing job %s to agent socket: %w", j.ID, err)
	}

	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading agent socket reply for job %s: %w", j.ID, err)
	}
	reply = strings.TrimSpace(reply)
	if reply != "ok" {
		return fmt.Errorf("agent rejected job %s: %s", j.ID, reply)
	}
	return nil
}

// Receiver is handed every Job a Server accepts. A real agent wires this to
// its own storage/forwarding path — this package doesn't assume one, same
// as transport.BatchReceiver on the hub side.
type Receiver interface {
	ReceiveJob(ctx context.Context, j Job) error
}

// Server accepts Job deliveries over a Unix socket (ADR-0010). Not wired
// into cmd/bitacora-agent yet — see internal/job's README for why that's a
// deliberate followup, not an oversight.
type Server struct {
	Receiver Receiver
}

// Serve accepts connections on ln until ctx is done or ln is closed,
// handling each one synchronously: one Job in, one reply out, connection
// closed. A slow or misbehaving client only blocks itself.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("accepting on job socket: %w", err)
		}
		s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		fmt.Fprintf(conn, "error: reading request: %v\n", err)
		return
	}

	var j Job
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &j); err != nil {
		fmt.Fprintf(conn, "error: decoding job: %v\n", err)
		return
	}

	if err := s.Receiver.ReceiveJob(ctx, j); err != nil {
		fmt.Fprintf(conn, "error: %v\n", err)
		return
	}

	fmt.Fprintln(conn, "ok")
}
