// SPDX-License-Identifier: Apache-2.0

package snapraid

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
		{[]string{"snapraid", "sync"}, true},
		{[]string{"/usr/bin/snapraid", "scrub"}, true},
		{[]string{"rsync", "-a"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		if got := (Extractor{}).Detect(tt.argv); got != tt.want {
			t.Errorf("Detect(%v) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}

func TestExtract_Sync(t *testing.T) {
	stdout, err := os.ReadFile("testdata/sync.txt")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	stats, errs := (Extractor{}).Extract(stdout, nil, 0)
	if len(errs) != 0 {
		t.Fatalf("unexpected extraction errors: %v", errs)
	}

	want := schema.JobStats{
		schema.StatFilesTransferred: int64(17), // 12 added + 5 updated
		schema.StatFilesDeleted:     int64(3),
		schema.StatFilesChecked:     int64(239), // 219 equal + 12 added + 3 removed + 5 updated
		schema.StatErrors:           int64(0),
	}
	for k, v := range want {
		if stats[k] != v {
			t.Errorf("stats[%q] = %v, want %v", k, stats[k], v)
		}
	}
}

func TestExtract_ScrubErrors(t *testing.T) {
	stdout, err := os.ReadFile("testdata/scrub.txt")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	stats, errs := (Extractor{}).Extract(stdout, nil, 0)
	if len(errs) != 0 {
		t.Fatalf("unexpected extraction errors: %v", errs)
	}
	if stats[schema.StatErrors] != int64(1) {
		t.Errorf("errors = %v, want 1", stats[schema.StatErrors])
	}
}

func TestExtract_NoSummary(t *testing.T) {
	_, errs := (Extractor{}).Extract([]byte("nothing useful\n"), nil, 0)
	if len(errs) == 0 {
		t.Fatal("expected an error when no summary is present")
	}
}
