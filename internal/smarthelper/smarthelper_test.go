package smarthelper

import (
	"context"
	"errors"
	"testing"
)

func TestRun_CollectsResultsFromEveryDevice(t *testing.T) {
	list := func() ([]string, error) { return []string{"sda", "nvme0n1"}, nil }
	run := func(ctx context.Context, device string) ([]byte, error) {
		return []byte(`{"device":"` + device + `"}`), nil
	}

	result, errs := Run(context.Background(), list, run)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(result.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(result.Devices))
	}
	if _, ok := result.Devices["sda"]; !ok {
		t.Fatal("expected sda in the result")
	}
}

func TestRun_OneFailingDeviceDoesNotBlockTheRest(t *testing.T) {
	list := func() ([]string, error) { return []string{"sda", "nvme0n1"}, nil }
	run := func(ctx context.Context, device string) ([]byte, error) {
		if device == "sda" {
			return nil, errors.New("device timed out")
		}
		return []byte(`{"device":"nvme0n1"}`), nil
	}

	result, errs := Run(context.Background(), list, run)

	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %v", errs)
	}
	if len(result.Devices) != 1 {
		t.Fatalf("expected the healthy device to still be reported, got %d devices", len(result.Devices))
	}
	if _, ok := result.Devices["nvme0n1"]; !ok {
		t.Fatal("expected nvme0n1 in the result despite sda failing")
	}
}

func TestRun_RejectsInvalidJSONOutput(t *testing.T) {
	list := func() ([]string, error) { return []string{"sda"}, nil }
	run := func(ctx context.Context, device string) ([]byte, error) {
		return []byte("not json"), nil
	}

	result, errs := Run(context.Background(), list, run)

	if len(errs) != 1 {
		t.Fatalf("expected 1 error for invalid JSON, got %v", errs)
	}
	if len(result.Devices) != 0 {
		t.Fatalf("expected no devices recorded for invalid JSON, got %d", len(result.Devices))
	}
}

func TestRun_ListerFailureIsReportedNotPanicked(t *testing.T) {
	list := func() ([]string, error) { return nil, errors.New("cannot read /sys/block") }
	run := func(ctx context.Context, device string) ([]byte, error) { return nil, nil }

	result, errs := Run(context.Background(), list, run)

	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %v", errs)
	}
	if len(result.Devices) != 0 {
		t.Fatalf("expected no devices, got %d", len(result.Devices))
	}
}
