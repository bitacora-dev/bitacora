// SPDX-License-Identifier: Apache-2.0

package rclone

import (
	"os"
	"testing"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{[]string{"rclone", "sync", "a", "b"}, true},
		{[]string{"/usr/bin/rclone", "sync"}, true},
		{[]string{"rsync", "-a"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		if got := (Extractor{}).Detect(tt.argv); got != tt.want {
			t.Errorf("Detect(%v) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}

func TestExtract_FinalStatsLineWins(t *testing.T) {
	stdout, err := os.ReadFile("testdata/sync.jsonlog")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	stats, errs := (Extractor{}).Extract(nil, stdout, 0)
	if len(errs) != 0 {
		t.Fatalf("unexpected extraction errors: %v", errs)
	}

	want := schema.JobStats{
		schema.StatFilesTransferred: int64(1284),
		schema.StatBytesTransferred: int64(44230118400),
		schema.StatFilesDeleted:     int64(12),
		schema.StatFilesChecked:     int64(98221),
		schema.StatErrors:           int64(0),
	}
	for k, v := range want {
		if stats[k] != v {
			t.Errorf("stats[%q] = %v, want %v", k, stats[k], v)
		}
	}
}

func TestExtract_NoStatsLines(t *testing.T) {
	stats, errs := (Extractor{}).Extract([]byte("plain text, no json here\n"), nil, 0)
	if len(errs) == 0 {
		t.Fatal("expected an error when no stats line is present")
	}
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %v", stats)
	}
}
