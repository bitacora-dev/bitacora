package main

import (
	"reflect"
	"testing"
	"time"
)

func TestParseArgs_MinimalValid(t *testing.T) {
	opts, err := parseArgs([]string{"--job", "rclone-aginsur-sync", "--", "rclone", "sync", "a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.JobName != "rclone-aginsur-sync" {
		t.Errorf("JobName = %q", opts.JobName)
	}
	if opts.Command != "rclone" {
		t.Errorf("Command = %q", opts.Command)
	}
	if !reflect.DeepEqual(opts.Args, []string{"sync", "a", "b"}) {
		t.Errorf("Args = %v", opts.Args)
	}
}

func TestParseArgs_AllFlags(t *testing.T) {
	opts, err := parseArgs([]string{
		"--job", "x", "--trigger", "cron", "--timeout", "90s",
		"--socket", "/tmp/a.sock", "--spool-dir", "/tmp/spool",
		"--", "echo", "hi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Trigger != "cron" {
		t.Errorf("Trigger = %q", opts.Trigger)
	}
	if opts.Timeout != 90*time.Second {
		t.Errorf("Timeout = %v", opts.Timeout)
	}
	if opts.SocketPath != "/tmp/a.sock" {
		t.Errorf("SocketPath = %q", opts.SocketPath)
	}
	if opts.SpoolDir != "/tmp/spool" {
		t.Errorf("SpoolDir = %q", opts.SpoolDir)
	}
}

func TestParseArgs_WrappedCommandFlagsPassThroughUntouched(t *testing.T) {
	opts, err := parseArgs([]string{"--job", "x", "--", "rclone", "sync", "--use-json-log", "--job", "not-ours"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"sync", "--use-json-log", "--job", "not-ours"}
	if !reflect.DeepEqual(opts.Args, want) {
		t.Errorf("Args = %v, want %v", opts.Args, want)
	}
}

func TestParseArgs_MissingJobIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"--", "echo", "hi"}); err == nil {
		t.Fatal("expected an error when --job is missing")
	}
}

func TestParseArgs_MissingCommandIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"--job", "x"}); err == nil {
		t.Fatal("expected an error when there's no `-- CMD` at all")
	}
	if _, err := parseArgs([]string{"--job", "x", "--"}); err == nil {
		t.Fatal("expected an error when `--` isn't followed by a command")
	}
}

func TestParseArgs_UnknownFlagIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"--job", "x", "--bogus", "--", "echo"}); err == nil {
		t.Fatal("expected an error for an unrecognized flag")
	}
}

func TestParseArgs_InvalidTimeoutIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"--job", "x", "--timeout", "not-a-duration", "--", "echo"}); err == nil {
		t.Fatal("expected an error for an unparseable --timeout")
	}
}

func TestParseArgs_FlagMissingValueIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"--job"}); err == nil {
		t.Fatal("expected an error when --job has no value")
	}
}
