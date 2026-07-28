package machine

import (
	"strings"
	"testing"
)

func TestRlimitDriftReportsNothingForAMatchingSpec(t *testing.T) {
	declared := map[string]string{"nofile": "1048576", "nproc": "infinity"}
	actuated := map[string]string{"nproc": "infinity", "nofile": "1048576"}
	if diffs := RlimitDrift(declared, actuated); diffs != nil {
		t.Errorf("RlimitDrift reported %v for two equal maps", diffs)
	}
}

// The common case is a machine that declares no limits at all, and it
// must never read as drift. Otherwise adding this field to the API
// would ask a whole fleet to reboot.
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
		actuated map[string]string
		want     string
	}{
		{
			name:     "added",
			declared: map[string]string{"nofile": "1048576"},
			actuated: nil,
			want:     "rlimit nofile: 1048576 declared but not actuated",
		},
		{
			name:     "retracted",
			declared: nil,
			actuated: map[string]string{"nofile": "1048576"},
			want:     "rlimit nofile: 1048576 actuated but no longer declared",
		},
		{
			name:     "changed",
			declared: map[string]string{"nofile": "2097152"},
			actuated: map[string]string{"nofile": "1048576"},
			want:     "rlimit nofile: 2097152 declared, 1048576 actuated",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diffs := RlimitDrift(c.declared, c.actuated)
			if len(diffs) != 1 {
				t.Fatalf("RlimitDrift = %v, want one difference", diffs)
			}
			if diffs[0] != c.want {
				t.Errorf("RlimitDrift = %q, want %q", diffs[0], c.want)
			}
		})
	}
}

// The diffs reach a person verbatim, in a condition message, so their
// order must not depend on map iteration.
func TestRlimitDriftReportsInASettledOrder(t *testing.T) {
	declared := map[string]string{"stack": "8388608", "nofile": "1048576", "core": "infinity"}
	for range 20 {
		diffs := RlimitDrift(declared, nil)
		got := strings.Join(diffs, "; ")
		want := "rlimit core: infinity declared but not actuated; " +
			"rlimit nofile: 1048576 declared but not actuated; " +
			"rlimit stack: 8388608 declared but not actuated"
		if got != want {
			t.Fatalf("RlimitDrift = %q, want %q", got, want)
		}
	}
}

// The limits that ship with the release are not part of the spec, so a
// boot that actuated them alone is converged with a spec that declares
// nothing. Otherwise every machine in a fleet would drift at once.
func TestRlimitDriftIgnoresTheLimitsThatShipWithTheRelease(t *testing.T) {
	if diffs := RlimitDrift(nil, nil); diffs != nil {
		t.Fatalf("RlimitDrift reported %v", diffs)
	}
	// A spec that repeats a value from the table still drifts against
	// a boot that recorded no spec, because the boot record carries
	// the request and this boot was asked for nothing.
	diffs := RlimitDrift(map[string]string{"nofile": OSRlimits["nofile"]}, nil)
	if len(diffs) != 1 {
		t.Errorf("RlimitDrift = %v, want one difference", diffs)
	}
}

func TestValidateRlimitsAcceptsAGoodSpec(t *testing.T) {
	spec := map[string]string{"nofile": "1048576", "nproc": "infinity", "core": "0:infinity"}
	if err := ValidateRlimits(spec); err != nil {
		t.Errorf("ValidateRlimits(%v): %v", spec, err)
	}
}

func TestValidateRlimitsAcceptsNoSpecAtAll(t *testing.T) {
	if err := ValidateRlimits(nil); err != nil {
		t.Errorf("ValidateRlimits(nil): %v", err)
	}
}

func TestValidateRlimitsRefusesAMisspelledResource(t *testing.T) {
	err := ValidateRlimits(map[string]string{"nofiles": "1048576"})
	if err == nil {
		t.Fatal("ValidateRlimits accepted a resource the kernel does not have")
	}
	if !strings.Contains(err.Error(), "nofiles") {
		t.Errorf("error %q does not name the entry that is wrong", err)
	}
}

func TestValidateRlimitsRefusesAValueThatWillNotParse(t *testing.T) {
	err := ValidateRlimits(map[string]string{"nofile": "lots"})
	if err == nil {
		t.Fatal("ValidateRlimits accepted a value that is not a number")
	}
	for _, want := range []string{"nofile", "lots"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
