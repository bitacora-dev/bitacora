package vpnhelper

import (
	"context"
	"errors"
	"testing"
)

func TestRun_CapturesBothCommands(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "wg":
			return []byte("wg0\tprivkey\tpubkey\t51820\toff\n"), nil
		case "tailscale":
			return []byte(`{"BackendState":"Running"}`), nil
		}
		return nil, errors.New("unexpected command")
	}

	result, errs := Run(context.Background(), run)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if result.WireguardDump == "" {
		t.Fatal("expected wireguard dump to be captured")
	}
	if result.TailscaleStatusJSON == "" {
		t.Fatal("expected tailscale status to be captured")
	}
}

func TestRun_OneCommandMissingIsNonFatal(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "wg" {
			return nil, errors.New("exec: \"wg\": executable file not found in $PATH")
		}
		return []byte(`{"BackendState":"Running"}`), nil
	}

	result, errs := Run(context.Background(), run)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error (wg missing), got %v", errs)
	}
	if result.WireguardDump != "" {
		t.Fatalf("expected no wireguard dump, got %q", result.WireguardDump)
	}
	if result.TailscaleStatusJSON == "" {
		t.Fatal("expected tailscale status to still be captured despite wg failing")
	}
}

func TestRun_BothCommandsMissing(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("not found")
	}

	result, errs := Run(context.Background(), run)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %v", errs)
	}
	if result.WireguardDump != "" || result.TailscaleStatusJSON != "" {
		t.Fatalf("expected an empty result, got %+v", result)
	}
}

func TestRun_CallsExactlyTheClosedCommandList(t *testing.T) {
	var calls [][]string
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return []byte("ok"), nil
	}

	Run(context.Background(), run)

	want := [][]string{
		{"wg", "show", "all", "dump"},
		{"tailscale", "status", "--json"},
	}
	if len(calls) != len(want) {
		t.Fatalf("expected exactly %d calls, got %d: %v", len(want), len(calls), calls)
	}
	for i, w := range want {
		if len(calls[i]) != len(w) {
			t.Fatalf("call %d: got %v, want %v", i, calls[i], w)
		}
		for j := range w {
			if calls[i][j] != w[j] {
				t.Fatalf("call %d: got %v, want %v", i, calls[i], w)
			}
		}
	}
}
