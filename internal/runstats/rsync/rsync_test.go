package rsync

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
		{[]string{"rsync", "-a", "--stats"}, true},
		{[]string{"/usr/bin/rsync", "-a"}, true},
		{[]string{"rclone", "sync"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		if got := (Extractor{}).Detect(tt.argv); got != tt.want {
			t.Errorf("Detect(%v) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}

func TestExtract_StatsSummary(t *testing.T) {
	stdout, err := os.ReadFile("testdata/stats.txt")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	stats, errs := (Extractor{}).Extract(stdout, nil, 0)
	if len(errs) != 0 {
		t.Fatalf("unexpected extraction errors: %v", errs)
	}

	want := schema.JobStats{
		schema.StatFilesTransferred: int64(1284),
		schema.StatBytesTransferred: int64(44230118400),
		schema.StatFilesDeleted:     int64(12),
		schema.StatFilesChecked:     int64(98233),
		schema.StatErrors:           int64(0),
	}
	for k, v := range want {
		if stats[k] != v {
			t.Errorf("stats[%q] = %v, want %v", k, stats[k], v)
		}
	}
}

func TestExtract_ErrorLinesCounted(t *testing.T) {
	stderr := []byte("rsync: send_files failed to open \"/x\": Permission denied (13)\nrsync error: some files/attrs were not transferred (code 23)\n")

	stats, _ := (Extractor{}).Extract(nil, stderr, 23)
	if stats[schema.StatErrors] != int64(2) {
		t.Errorf("errors = %v, want 2", stats[schema.StatErrors])
	}
}

func TestExtract_NoStatsSummary(t *testing.T) {
	_, errs := (Extractor{}).Extract([]byte("nothing useful here\n"), nil, 0)
	if len(errs) == 0 {
		t.Fatal("expected an error when no stats summary is present")
	}
}
