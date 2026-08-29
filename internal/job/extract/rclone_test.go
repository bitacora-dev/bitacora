package extract

import "testing"

func TestRclone_Detect(t *testing.T) {
	r := Rclone{}
	if !r.Detect("rclone", []string{"sync", "/mnt/storage/aginsur", "remote:aginsur", "--use-json-log"}) {
		t.Fatal("expected rclone to be detected")
	}
	if r.Detect("rsync", []string{"-a"}) {
		t.Fatal("expected rsync not to be detected as rclone")
	}
}

// rcloneJSONLogFixture is a synthetic but format-accurate rclone
// --use-json-log transcript: one JSON object per line, with the final
// stats line carrying the cumulative "stats" object rclone emits at
// --stats intervals and on completion.
const rcloneJSONLogFixture = `{"level":"info","msg":"Starting sync","source":"sync/sync.go:100","time":"2026-08-28T02:00:00.000000-00:00"}
{"level":"notice","msg":"","source":"accounting/stats.go:500","stats":{"bytes":10000,"checks":50,"deletes":0,"errors":0,"renames":0,"transfers":10},"time":"2026-08-28T02:20:00.000000-00:00"}
{"level":"notice","msg":"","source":"accounting/stats.go:500","stats":{"bytes":44230118400,"checks":98221,"deletes":12,"errors":0,"renames":3,"transfers":1284},"time":"2026-08-28T02:41:33.000000-00:00"}
{"level":"info","msg":"Sync complete","source":"sync/sync.go:150","time":"2026-08-28T02:41:33.000000-00:00"}
`

func TestRclone_ExtractsFromFinalStatsLine(t *testing.T) {
	stats, err := Rclone{}.Extract(nil, []byte(rcloneJSONLogFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]int64{
		"bytes_transferred": 44230118400,
		"files_checked":     98221,
		"files_deleted":     12,
		"errors":            0,
		"files_renamed":     3,
		"files_transferred": 1284,
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

func TestRclone_TolerantOfNonJSONLines(t *testing.T) {
	mixed := "not json at all\n" + rcloneJSONLogFixture
	stats, err := Rclone{}.Extract(nil, []byte(mixed))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats["files_transferred"] != int64(1284) {
		t.Fatalf("expected the valid stats line to still be parsed, got %v", stats)
	}
}

func TestRclone_NoStatsLineReturnsEmptyStats(t *testing.T) {
	stats, err := Rclone{}.Extract(nil, []byte(`{"level":"info","msg":"nothing here"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected no stats, got %v", stats)
	}
}
