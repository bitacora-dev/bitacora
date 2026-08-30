// Package debversion implements Debian's own package version comparison
// algorithm (Debian Policy Manual §5.6.12) — used by ADR-0017's apt
// source to decide whether a candidate version is genuinely newer than
// what's installed. This is deliberately not a plain string comparison:
// Debian version schemes have their own rules ("1.9" < "1.10" numerically,
// "~" sorts before everything including the end of a string, so
// "1.0~beta1" < "1.0") that a naive comparison gets predictably wrong.
//
// Rather than pull in a third-party dependency for an algorithm this
// small and this precisely specified, it's implemented directly here
// against the Policy Manual's own description and worked examples.
package debversion

import "strings"

// Compare returns -1, 0 or 1 as a's version is less than, equal to, or
// greater than b's, using dpkg's own version-ordering rules.
func Compare(a, b string) int {
	epochA, restA := splitEpoch(a)
	epochB, restB := splitEpoch(b)
	if c := compareDigitRun(epochA, epochB); c != 0 {
		return c
	}

	upstreamA, revisionA := splitUpstreamRevision(restA)
	upstreamB, revisionB := splitUpstreamRevision(restB)
	if c := compareVersionPart(upstreamA, upstreamB); c != 0 {
		return c
	}
	return compareVersionPart(revisionA, revisionB)
}

// splitEpoch separates a leading "N:" epoch from the rest of the version
// string. A version with no epoch is treated as epoch "0", per Policy.
func splitEpoch(v string) (epoch, rest string) {
	if idx := strings.IndexByte(v, ':'); idx >= 0 {
		return v[:idx], v[idx+1:]
	}
	return "0", v
}

// splitUpstreamRevision separates the upstream_version from the
// debian_revision at the LAST '-' (upstream versions may themselves
// contain '-'). A version with no '-' has no debian_revision; per
// Policy that compares the same as an explicit "0" would, which falls
// out naturally from compareVersionPart("", "0") == 0.
func splitUpstreamRevision(v string) (upstream, revision string) {
	if idx := strings.LastIndexByte(v, '-'); idx >= 0 {
		return v[:idx], v[idx+1:]
	}
	return v, ""
}

// compareVersionPart implements the core alternating algorithm Policy
// describes for both upstream_version and debian_revision: alternately
// compare a run of non-digit characters, then a run of digit characters
// (numerically), left to right, until a difference is found or both
// strings are exhausted.
func compareVersionPart(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		startI, startJ := i, j
		for i < len(a) && !isDigit(a[i]) {
			i++
		}
		for j < len(b) && !isDigit(b[j]) {
			j++
		}
		if c := compareNonDigitRun(a[startI:i], b[startJ:j]); c != 0 {
			return c
		}

		startI, startJ = i, j
		for i < len(a) && isDigit(a[i]) {
			i++
		}
		for j < len(b) && isDigit(b[j]) {
			j++
		}
		if c := compareDigitRun(a[startI:i], b[startJ:j]); c != 0 {
			return c
		}
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// compareNonDigitRun compares two non-digit runs character by character
// using dpkg's own ordering: '~' sorts before everything (even the end of
// a string), letters sort before every other character, and within each
// of those groups characters compare by ASCII value. A run shorter than
// the other is padded with a sentinel byte (order 0) so "end of string"
// takes its correct place between '~' and everything else.
func compareNonDigitRun(a, b string) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for k := 0; k < n; k++ {
		var ca, cb byte
		if k < len(a) {
			ca = a[k]
		}
		if k < len(b) {
			cb = b[k]
		}
		if oa, ob := charOrder(ca), charOrder(cb); oa != ob {
			if oa < ob {
				return -1
			}
			return 1
		}
	}
	return 0
}

// charOrder implements dpkg's order() weighting: '~' < end-of-string <
// letters (by ASCII value) < everything else (by ASCII value, offset
// above every letter).
func charOrder(c byte) int {
	switch {
	case c == '~':
		return -1
	case c == 0: // sentinel for "no character here" (end of string)
		return 0
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		return int(c)
	default:
		return int(c) + 256
	}
}

// compareDigitRun compares two runs of digits (or epochs) numerically,
// treating an empty run as 0 and ignoring leading zeros — long enough
// digit runs are compared as strings after stripping leading zeros
// (equal length then decides by lexical order, which is numeric order
// for equal-length digit strings) rather than parsed into a machine
// integer, so an implausibly long version segment still compares
// correctly instead of overflowing.
func compareDigitRun(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
