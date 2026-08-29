package alerting

import (
	"testing"
	"time"
)

func TestNewSilence_RejectsZeroEndTime(t *testing.T) {
	_, err := NewSilence(map[string]string{"host_id": "host-a"}, time.Now(), time.Time{}, "nacho", "maintenance")
	if err == nil {
		t.Fatal("expected an error for a silence with no end time")
	}
}

func TestNewSilence_RejectsEndBeforeStart(t *testing.T) {
	now := time.Now()
	_, err := NewSilence(map[string]string{"host_id": "host-a"}, now, now.Add(-time.Hour), "nacho", "maintenance")
	if err == nil {
		t.Fatal("expected an error when end is before start")
	}
}

func TestSilenceStore_SuppressesMatchingLabelsWithinWindow(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now()
	sil, err := NewSilence(map[string]string{"host_id": "host-a"}, now.Add(-time.Minute), now.Add(time.Hour), "nacho", "maintenance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.Add(sil)

	if !store.Silenced(now, map[string]string{"host_id": "host-a", "rule": "cpu-temp"}) {
		t.Fatal("expected a matching, in-window silence to suppress")
	}
}

func TestSilenceStore_DoesNotSuppressAfterExpiry(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now()
	sil, err := NewSilence(map[string]string{"host_id": "host-a"}, now.Add(-time.Hour), now.Add(-time.Minute), "nacho", "past maintenance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.Add(sil)

	if store.Silenced(now, map[string]string{"host_id": "host-a"}) {
		t.Fatal("expected an expired silence not to suppress")
	}
}

func TestSilenceStore_DoesNotSuppressNonMatchingLabels(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now()
	sil, err := NewSilence(map[string]string{"host_id": "host-a"}, now.Add(-time.Minute), now.Add(time.Hour), "nacho", "maintenance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.Add(sil)

	if store.Silenced(now, map[string]string{"host_id": "host-b"}) {
		t.Fatal("expected a silence for host-a not to suppress host-b")
	}
}

func TestSilenceStore_RemoveStopsSuppressing(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now()
	sil, err := NewSilence(map[string]string{"host_id": "host-a"}, now.Add(-time.Minute), now.Add(time.Hour), "nacho", "maintenance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.Add(sil)
	store.Remove(sil.ID)

	if store.Silenced(now, map[string]string{"host_id": "host-a"}) {
		t.Fatal("expected a removed silence not to suppress")
	}
}

func TestSilence_EmptyMatchersMatchesNothing(t *testing.T) {
	sil := Silence{Matchers: map[string]string{}}
	if sil.Matches(map[string]string{"host_id": "host-a"}) {
		t.Fatal("expected a silence with no matchers to match nothing — it must name what it silences")
	}
}
