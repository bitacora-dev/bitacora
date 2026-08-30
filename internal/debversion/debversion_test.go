package debversion

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.9", "1.10", -1}, // numeric, not lexical, comparison of digit runs
		{"1.10", "1.9", 1},
		{"1.0", "1.0.1", -1},
		{"1.0~beta1", "1.0", -1}, // tilde sorts before everything, even nothing
		{"1.0~~", "1.0~~a", -1},  // shorter tilde-run still sorts first
		{"1.0~", "1.0", -1},      // "~" < "" (end of string)
		{"1.0-1", "1.0-2", -1},   // debian_revision compared same way
		{"1.0-1", "1.0", 1},      // explicit "-1" beats implicit "-0"
		{"1:1.0", "2.0", 1},      // epoch always wins first
		{"1:1.0", "1:2.0", -1},
		{"0:1.0", "1.0", 0}, // explicit epoch 0 == no epoch
		{"1.0+git1", "1.0", 1},
		{"1.0a", "1.0", 1}, // letters sort earlier than end-of-string, but only among non-digit chars after a matching prefix — 'a' here is compared against nothing (end of string), and 'a' has positive order
		{"2.0", "1.0", 1},
		{"1.0-0ubuntu1", "1.0-1", -1}, // 'u' (117) loses to digit-run comparison of "0" prefix — exercises alternation
	}

	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		// Compare must be antisymmetric.
		if want := -c.want; c.want != 0 {
			if got := Compare(c.b, c.a); got != want {
				t.Errorf("Compare(%q, %q) = %d, want %d (antisymmetric to Compare(%q, %q))", c.b, c.a, got, want, c.a, c.b)
			}
		}
	}
}

func TestCompare_TildeSortsBeforeEverythingIncludingEmptyString(t *testing.T) {
	// Direct Policy Manual example: "~~", "~~a", "~", "" (empty tail),
	// "a" must sort in exactly this ascending order.
	ordered := []string{"1.0~~", "1.0~~a", "1.0~", "1.0", "1.0a"}
	for i := 0; i < len(ordered)-1; i++ {
		if c := Compare(ordered[i], ordered[i+1]); c >= 0 {
			t.Errorf("expected %q < %q, got Compare=%d", ordered[i], ordered[i+1], c)
		}
	}
}
