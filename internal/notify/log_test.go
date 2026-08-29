package notify

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLogNotifier_WritesBodyToWriter(t *testing.T) {
	var buf bytes.Buffer
	notifier := &LogNotifier{Writer: &buf}

	if err := notifier.Notify(context.Background(), sampleNotification()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "cpu-temp-alta") {
		t.Fatalf("expected the log line to contain the rule id, got %q", buf.String())
	}
}

func TestLogNotifier_DefaultsToStderrWithoutPanicking(t *testing.T) {
	notifier := &LogNotifier{}
	if err := notifier.Notify(context.Background(), sampleNotification()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
