package main

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
		ok   bool
	}{
		{"equal releases", "2026.09.03-007", "2026.09.03-007", 0, true},
		{"older serial", "2026.09.03-006", "2026.09.03-007", -1, true},
		{"newer serial", "2026.09.03-008", "2026.09.03-007", 1, true},
		{"older day", "2026.09.02-999", "2026.09.03-001", -1, true},
		{"older month", "2026.08.31-999", "2026.09.01-001", -1, true},
		{"older year", "2025.12.31-999", "2026.01.01-001", -1, true},
		{"release before its dev build", "2026.09.03-007", "2026.09.03-007-dev-003-abcdef01", -1, true},
		{"dev build before next release", "2026.09.03-007-dev-003-abcdef01", "2026.09.03-008", -1, true},
		{"lower dev count first", "2026.09.03-007-dev-002-aa", "2026.09.03-007-dev-003-bb", -1, true},
		{"equal dev builds", "2026.09.03-007-dev-003-aa", "2026.09.03-007-dev-003-bb", 0, true},
		{"dev on the left does not order", "dev", "2026.09.03-007", 0, false},
		{"dev on the right does not order", "2026.09.03-007", "dev", 0, false},
		{"a missing serial does not order", "2026.09.03", "2026.09.03-007", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := compareVersions(tc.a, tc.b)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("compareVersions(%q, %q) = (%d, %v), want (%d, %v)", tc.a, tc.b, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDecideVersionAction(t *testing.T) {
	cases := []struct {
		name       string
		cli        string
		operator   string
		minimumCLI string
		want       versionAction
	}{
		{"same version runs", "2026.09.03-007", "2026.09.03-007", "", actionRun},
		{"unknown operator runs", "2026.09.03-007", "", "", actionRun},
		{"drift warns", "2026.09.03-006", "2026.09.03-007", "", actionWarn},
		{"below the floor refuses", "2026.09.03-006", "2026.09.03-007", "2026.09.03-007", actionRefuse},
		{"at the floor runs", "2026.09.03-007", "2026.09.03-007", "2026.09.03-007", actionRun},
		{"a dev build ignores the floor", "dev", "2026.09.03-007", "2026.09.03-007", actionWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, message := decideVersionAction(tc.cli, tc.operator, tc.minimumCLI)
			if got != tc.want {
				t.Fatalf("decideVersionAction(%q, %q, %q) = %d, want %d", tc.cli, tc.operator, tc.minimumCLI, got, tc.want)
			}
			if (got == actionRun) != (message == "") {
				t.Fatalf("a run carries no message and a warn or refuse carries one; got action %d with message %q", got, message)
			}
		})
	}
}
