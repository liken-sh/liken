package machine

import (
	"strings"
	"testing"
)

func TestRlimitDriftReportsNothingForAMatchingSpec(t *testing.T) {
	declared := map[string]string{"nofile": "1048576", "nproc": "infinity"}
	booted := map[string]string{"nproc": "infinity", "nofile": "1048576"}
	if diffs := RlimitDrift(declared, booted); diffs != nil {
		t.Errorf("RlimitDrift reported %v for two equal maps", diffs)
	}
}

// Most machines declare no limits, and that must not read as drift.
func TestRlimitDriftReportsNothingWhenNeitherSideDeclaresAnything(t *testing.T) {
	if diffs := RlimitDrift(nil, nil); diffs != nil {
		t.Errorf("RlimitDrift reported %v for two empty maps", diffs)
	}
	if diffs := RlimitDrift(map[string]string{}, nil); diffs != nil {
		t.Errorf("RlimitDrift reported %v for an empty declared map", diffs)
	}
}

func TestRlimitDriftReportsEachKindOfDifference(t *testing.T) {
	cases := []struct {
		name     string
		declared map[string]string
		booted   map[string]string
		want     string
	}{
		{
			name:     "added",
			declared: map[string]string{"nofile": "1048576"},
			booted:   nil,
			want:     "rlimit nofile: 1048576 declared but this boot ran without it",
		},
		{
			name:     "retracted",
			declared: nil,
			booted:   map[string]string{"nofile": "1048576"},
			want:     "rlimit nofile: 1048576 no longer declared but this boot ran with it",
		},
		{
			name:     "changed",
			declared: map[string]string{"nofile": "2097152"},
			booted:   map[string]string{"nofile": "1048576"},
			want:     "rlimit nofile: 2097152 declared, 1048576 in this boot",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diffs := RlimitDrift(c.declared, c.booted)
			if len(diffs) != 1 {
				t.Fatalf("RlimitDrift = %v, want one difference", diffs)
			}
			if diffs[0] != c.want {
				t.Errorf("RlimitDrift = %q, want %q", diffs[0], c.want)
			}
		})
	}
}

// The differences appear in a condition message, so their order must
// not change from run to run with map iteration.
func TestRlimitDriftReportsInASettledOrder(t *testing.T) {
	declared := map[string]string{"stack": "8388608", "nofile": "1048576", "core": "infinity"}
	for range 20 {
		diffs := RlimitDrift(declared, nil)
		got := strings.Join(diffs, "; ")
		want := "rlimit core: infinity declared but this boot ran without it; " +
			"rlimit nofile: 1048576 declared but this boot ran without it; " +
			"rlimit stack: 8388608 declared but this boot ran without it"
		if got != want {
			t.Fatalf("RlimitDrift = %q, want %q", got, want)
		}
	}
}

// The limits that ship with the release are not part of the spec, so
// a boot that applied only those limits matches a spec that declares
// none.
func TestRlimitDriftIgnoresTheLimitsThatShipWithTheRelease(t *testing.T) {
	if diffs := RlimitDrift(nil, nil); diffs != nil {
		t.Fatalf("RlimitDrift = %v, want no difference", diffs)
	}
	// A spec that repeats a release limit still differs from a boot
	// that declared nothing, because the boot record holds only what
	// the spec declared.
	diffs := RlimitDrift(map[string]string{"nofile": OSRlimits["nofile"]}, nil)
	if len(diffs) != 1 {
		t.Errorf("RlimitDrift = %v, want one difference", diffs)
	}
}

func TestValidateRlimitsAcceptsAGoodSpec(t *testing.T) {
	spec := map[string]string{"nofile": "1048576", "nproc": "infinity", "core": "0:infinity"}
	if err := ValidateRlimits(spec); err != nil {
		t.Errorf("ValidateRlimits(%v) returned %v, want nil", spec, err)
	}
}

func TestValidateRlimitsAcceptsNoSpecAtAll(t *testing.T) {
	if err := ValidateRlimits(nil); err != nil {
		t.Errorf("ValidateRlimits(nil) returned %v, want nil", err)
	}
}

func TestValidateRlimitsRefusesAMisspelledResource(t *testing.T) {
	err := ValidateRlimits(map[string]string{"nofiles": "1048576"})
	if err == nil {
		t.Fatal("ValidateRlimits returned nil for a resource the kernel does not have, want an error")
	}
	if !strings.Contains(err.Error(), "nofiles") {
		t.Errorf("the error is %q, want it to name nofiles", err)
	}
}

func TestValidateRlimitsRefusesAValueThatWillNotParse(t *testing.T) {
	err := ValidateRlimits(map[string]string{"nofile": "lots"})
	if err == nil {
		t.Fatal("ValidateRlimits returned nil for a value that is not a number, want an error")
	}
	for _, want := range []string{"nofile", "lots"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to mention %q", err, want)
		}
	}
}
