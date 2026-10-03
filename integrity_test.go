package main

// The integrity level is measured rather than assumed, so it is worth one test that it is
// measured at all. What it comes out as depends on whoever is running the tests - a normal
// account gets "medium", a sandboxed one "low" - so only the vocabulary is pinned here, and the
// value is printed for whoever is reading the output.

import "testing"

func TestProcessIntegrity(t *testing.T) {
	level := processIntegrity()
	t.Logf("this process runs at integrity %q", level)
	switch level {
	case "untrusted", "low", "medium", "high", "system":
	default:
		t.Errorf("processIntegrity() = %q, which is not one of the levels this program names", level)
	}
}
