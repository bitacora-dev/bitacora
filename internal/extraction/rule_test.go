package extraction

import "testing"

func TestParse_ValidRule(t *testing.T) {
	r, err := Parse([]byte(`
id: test-rule
source: kernel
match: 'hello (?P<name>\w+)'
emit:
  type: test.event
  severity: info
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.ID != "test-rule" || r.Emit.Type != "test.event" {
		t.Fatalf("unexpected parsed rule: %+v", r)
	}
}

func TestParse_RejectsMissingID(t *testing.T) {
	_, err := Parse([]byte(`
match: 'x'
emit:
  type: t
  severity: info
`))
	if err == nil {
		t.Fatal("expected an error for a rule with no id")
	}
}

func TestParse_RejectsMissingMatch(t *testing.T) {
	_, err := Parse([]byte(`
id: r
emit:
  type: t
  severity: info
`))
	if err == nil {
		t.Fatal("expected an error for a rule with no match pattern")
	}
}

func TestParse_RejectsInvalidRegex(t *testing.T) {
	_, err := Parse([]byte(`
id: r
match: '(unclosed'
emit:
  type: t
  severity: info
`))
	if err == nil {
		t.Fatal("expected an error for an invalid regex")
	}
}

func TestParse_RejectsUnknownEnricher(t *testing.T) {
	_, err := Parse([]byte(`
id: r
match: 'x'
emit:
  type: t
  severity: info
  enrich: [not_a_real_enricher]
`))
	if err == nil {
		t.Fatal("expected an error for an unknown enricher name — a typo here shouldn't fail silently")
	}
}

func TestParse_RejectsMissingEmitType(t *testing.T) {
	_, err := Parse([]byte(`
id: r
match: 'x'
emit:
  severity: info
`))
	if err == nil {
		t.Fatal("expected an error for a rule with no emit.type")
	}
}

func TestLoadDir_LoadsKernelSegfaultRule(t *testing.T) {
	rules, err := LoadDir("rules")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("expected at least the kernel-segfault rule to load")
	}

	found := false
	for _, r := range rules {
		if r.ID == "kernel-segfault" {
			found = true
			if r.Emit.Type != "kernel.segfault" {
				t.Fatalf("expected type kernel.segfault, got %q", r.Emit.Type)
			}
		}
	}
	if !found {
		t.Fatal("expected the kernel-segfault rule to be present")
	}
}

func TestLoadDir_MissingDirIsAnError(t *testing.T) {
	if _, err := LoadDir("testdata/does-not-exist"); err == nil {
		t.Fatal("expected an error for a missing rules directory")
	}
}
