package dnfhelper

import (
	"context"
	"errors"
	"testing"
)

const realCheckUpdateOutput = `Last metadata expiration check: 0:12:34 ago on Mon 01 Jan 2026.
bash.x86_64                             5.1.8-6.el9                  baseos
kernel.x86_64                           5.14.0-284.11.1.el9_2        baseos
kernel-core.x86_64                      5.14.0-284.11.1.el9_2        baseos

Obsoleting Packages
newpkg.x86_64                           2.0-1.el9                    appstream
    obsoletes oldpkg.x86_64 < 1.5-1.el9
`

func TestRun_ParsesRealCheckUpdateOutput(t *testing.T) {
	run := func(ctx context.Context) ([]byte, bool, error) {
		return []byte(realCheckUpdateOutput), true, errors.New("exit status 100")
	}

	result, errs := Run(context.Background(), run)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for exit code 100 (updates available), got %v", errs)
	}
	if len(result.Updates) != 3 {
		t.Fatalf("expected 3 updates (obsoleting section excluded), got %d: %+v", len(result.Updates), result.Updates)
	}

	want := Update{Name: "bash", Arch: "x86_64", Version: "5.1.8-6.el9", Repo: "baseos"}
	if result.Updates[0] != want {
		t.Fatalf("unexpected first update: %+v, want %+v", result.Updates[0], want)
	}

	for _, u := range result.Updates {
		if u.Name == "newpkg" {
			t.Fatalf("expected the Obsoleting Packages section to be excluded, got %+v", u)
		}
	}
}

func TestRun_NoUpdatesIsNotAnError(t *testing.T) {
	run := func(ctx context.Context) ([]byte, bool, error) {
		return []byte("Last metadata expiration check: 0:01:00 ago.\n"), false, nil // exit 0
	}

	result, errs := Run(context.Background(), run)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(result.Updates) != 0 {
		t.Fatalf("expected no updates, got %+v", result.Updates)
	}
}

func TestRun_GenuineFailureIsReported(t *testing.T) {
	run := func(ctx context.Context) ([]byte, bool, error) {
		return nil, false, errors.New("exec: \"dnf\": executable file not found in $PATH")
	}

	_, errs := Run(context.Background(), run)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error for a genuine failure, got %v", errs)
	}
}

func TestSplitNameArch(t *testing.T) {
	cases := map[string]struct {
		name, arch string
		ok         bool
	}{
		"bash.x86_64":        {"bash", "x86_64", true},
		"kernel-core.noarch": {"kernel-core", "noarch", true},
		"no-dot":             {"", "", false},
		".leadingdot":        {"", "", false},
		"trailingdot.":       {"", "", false},
	}
	for input, want := range cases {
		name, arch, ok := splitNameArch(input)
		if name != want.name || arch != want.arch || ok != want.ok {
			t.Errorf("splitNameArch(%q) = (%q, %q, %v), want (%q, %q, %v)", input, name, arch, ok, want.name, want.arch, want.ok)
		}
	}
}
