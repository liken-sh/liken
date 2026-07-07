package machine

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.1.1", -1},
		{"0.1.1", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"0.2.0", "1.0.0", -1},
		// The reason the comparison is numeric, not lexicographic: as
		// strings, "0.10.0" sorts before "0.9.0".
		{"0.10.0", "0.9.0", 1},
		// A missing segment counts as zero.
		{"1.0", "1.0.0", 0},
		{"1.0.0.1", "1.0.0", 1},
		// A pre-release sorts before the release it precedes.
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0-rc1", "1.0.0-rc1", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q): got %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNewestVersion(t *testing.T) {
	catalog := []ReleaseCatalogEntry{
		{Version: "0.9.0"},
		{Version: "0.10.0"},
		{Version: "0.2.0"},
	}
	if got := NewestVersion(catalog); got != "0.10.0" {
		t.Errorf("got %q", got)
	}
	if got := NewestVersion(nil); got != "" {
		t.Errorf("an empty catalog has no newest; got %q", got)
	}
}
