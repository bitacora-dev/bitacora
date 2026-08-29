package extract

import "testing"

func TestRsync_Detect(t *testing.T) {
	r := Rsync{}
	if !r.Detect("rsync", []string{"-a", "--stats", "/src/", "/dst/"}) {
		t.Fatal("expected rsync --stats to be detected")
	}
	if r.Detect("rsync", []string{"-a", "/src/", "/dst/"}) {
		t.Fatal("expected rsync WITHOUT --stats not to be detected (no structured output to parse)")
	}
	if r.Detect("rclone", []string{"--stats"}) {
		t.Fatal("expected rclone not to be detected as rsync")
	}
}

// rsyncStatsFixture reproduces the real shape of `rsync -a --stats`'s
// summary block, thousands-separator commas included.
const rsyncStatsFixture = `Number of files: 98,221 (reg: 90,000, dir: 8,221)
Number of created files: 0
Number of deleted files: 12
Number of regular files transferred: 1,284
Total file size: 44,230,118,400 bytes
Total transferred file size: 44,230,118,400 bytes
Literal data: 44,230,118,400 bytes
Matched data: 0 bytes
File list size: 1,234
File list generation time: 0.010 seconds
File list transfer time: 0.000 seconds
Total bytes sent: 44,230,120,000
Total bytes received: 20,412

sent 44,230,120,000 bytes  received 20,412 bytes  1,234,567.89 bytes/sec
total size is 44,230,118,400  speedup is 1.00
`

func TestRsync_ExtractsFromStatsBlock(t *testing.T) {
	stats, err := Rsync{}.Extract([]byte(rsyncStatsFixture), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]int64{
		"files_checked":     98221,
		"files_transferred": 1284,
		"files_deleted":     12,
		"bytes_transferred": 44230118400,
	}
	for key, wantVal := range want {
		got, ok := stats[key]
		if !ok {
			t.Errorf("expected stats[%q] to be set, got %v", key, stats)
			continue
		}
		if got != wantVal {
			t.Errorf("stats[%q] = %v, want %d", key, got, wantVal)
		}
	}
}

func TestRsync_CountsErrorPrefixedStderrLines(t *testing.T) {
	stderr := "rsync: mkdir \"/dst/x\" failed: Permission denied (13)\n" +
		"rsync error: some files/attrs were not transferred (code 23)\n"

	stats, err := Rsync{}.Extract([]byte(rsyncStatsFixture), []byte(stderr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats["errors"] != int64(2) {
		t.Fatalf("expected 2 error lines counted, got %v", stats["errors"])
	}
}

func TestRsync_MissingFieldsAreSimplyAbsent(t *testing.T) {
	stats, err := Rsync{}.Extract([]byte("unexpected output format\n"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := stats["files_transferred"]; ok {
		t.Fatalf("expected no files_transferred when the field wasn't found, got %v", stats)
	}
}
