package api

import "testing"

func TestValidVersionAcceptsTheGrammar(t *testing.T) {
	for _, v := range []string{
		"2026.07.11-001",
		"2026.01.01-001",
		"2026.12.31-999",
	} {
		if err := ValidVersion(v); err != nil {
			t.Errorf("ValidVersion(%q): %v", v, err)
		}
	}
}

func TestValidVersionRejectsOtherShapes(t *testing.T) {
	for _, v := range []string{
		"",
		"0.1.0",             // semver, the shape liken does not use
		"2026.7.11-001",     // month must be zero-padded
		"2026.07.1-001",     // day must be zero-padded
		"2026.07.11",        // the serial is not optional
		"2026.07.11-1",      // serial must be three digits
		"2026.07.11-0001",   // exactly three digits
		"v2026.07.11-001",   // the v prefix belongs to the git tag only
		"2026.07.11-001-rc", // nothing after the serial
		"2026.13.01-001",    // not a real month
		"2026.02.30-001",    // not a real day
	} {
		if err := ValidVersion(v); err == nil {
			t.Errorf("ValidVersion(%q): want an error, got nil", v)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026.07.11-001", "2026.07.11-001", 0},
		{"2026.07.11-001", "2026.07.11-002", -1},
		{"2026.07.11-002", "2026.07.11-001", 1},
		// Zero-padding makes plain string comparison correct.
		// Without it, "010" and "9" would sort backwards.
		{"2026.07.11-009", "2026.07.11-010", -1},
		{"2026.07.09-002", "2026.07.10-001", -1},
		{"2026.09.30-001", "2026.10.01-001", -1},
		{"2026.12.31-001", "2027.01.01-001", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q): got %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
