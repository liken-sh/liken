package main

import (
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// These tests read the limits of a child process, not of the test
// process, because the two can differ. At startup the Go runtime
// raises its own soft RLIMIT_NOFILE to one below the hard limit and
// saves the original value, and os/exec gives every child that
// original. syscall.Setrlimit clears the saved value, so the child
// gets the new limit, but a call that bypasses it leaves the child
// with the original.
//
// The child is cat, because a Go child would raise its own soft limit
// at startup and report that.

// childLimits runs cat on /proc/self/limits and returns the soft and
// hard values the child inherited for one row of that table.
func childLimits(t *testing.T, row string) (soft, hard string) {
	t.Helper()
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("no cat to read /proc/self/limits from a non-Go child")
	}
	out, err := exec.Command(cat, "/proc/self/limits").Output()
	if err != nil {
		t.Fatalf("reading a child's limits: %v", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if !strings.HasPrefix(line, row) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, row))
		if len(fields) < 2 {
			t.Fatalf("the %q row of /proc/self/limits is %q, want a soft and a hard value", row, line)
		}
		return fields[0], fields[1]
	}
	t.Fatalf("/proc/self/limits of a child has no %q row", row)
	return "", ""
}

// keepRlimit restores a limit when the test ends. An unprivileged
// process cannot raise a hard limit that it lowered, so the tests never
// lower the hard limit.
func keepRlimit(t *testing.T, resource string) unix.Rlimit {
	t.Helper()
	n, err := machine.RlimitResource(resource)
	if err != nil {
		t.Fatalf("finding the resource of %s: %v", resource, err)
	}
	var saved unix.Rlimit
	if err := unix.Getrlimit(n, &saved); err != nil {
		t.Fatalf("reading the %s limit: %v", resource, err)
	}
	t.Cleanup(func() {
		if err := unix.Setrlimit(n, &saved); err != nil {
			t.Errorf("restoring the %s limit: %v", resource, err)
		}
	})
	return saved
}

// hardNofile returns the test process's nofile limits, and the hard
// limit written as a spec value.
func hardNofile(t *testing.T) (unix.Rlimit, string) {
	t.Helper()
	saved := keepRlimit(t, "nofile")
	if saved.Max == unix.RLIM_INFINITY {
		return saved, machine.RlimitInfinity
	}
	if saved.Max < 512 {
		t.Skipf("hard nofile limit is %d, too low to test against", saved.Max)
	}
	return saved, machine.FormatRlimit(unix.Rlimit{Cur: saved.Max, Max: saved.Max})
}

func TestAChildInheritsTheSoftLimitInitApplied(t *testing.T) {
	_, hard := hardNofile(t)

	applyRlimits(map[string]string{"nofile": "256:" + hard})

	gotSoft, gotHard := childLimits(t, "Max open files")
	if gotSoft != "256" {
		t.Errorf("a child inherited a soft nofile of %s, want 256, because k3s must receive the limit through exec", gotSoft)
	}
	if gotHard != hard {
		t.Errorf("a child inherited a hard nofile of %s, want %s", gotHard, hard)
	}
}

// A program that raises its own soft limit can raise it only to the
// hard limit, so the hard limit must reach the child too.
func TestAChildInheritsAHardLimitEqualToItsSoftLimit(t *testing.T) {
	_, hard := hardNofile(t)

	applyRlimits(map[string]string{"nofile": hard})

	gotSoft, gotHard := childLimits(t, "Max open files")
	if gotSoft != hard || gotHard != hard {
		t.Errorf("a child inherited nofile %s:%s, want %s:%s", gotSoft, gotHard, hard, hard)
	}
}

func TestApplyRlimitsSkipsABadEntryAndKeepsGoing(t *testing.T) {
	_, hard := hardNofile(t)

	applyRlimits(map[string]string{
		"nofiles": "1048576", // misspelled: no such resource
		"nofile":  "256:" + hard,
		"nproc":   "not a number",
	})

	if gotSoft, _ := childLimits(t, "Max open files"); gotSoft != "256" {
		t.Errorf("soft nofile = %s, want 256, because a bad entry must not stop the other entries", gotSoft)
	}
}

func TestReadRlimitsReportsWhatTheKernelHolds(t *testing.T) {
	_, hard := hardNofile(t)

	applyRlimits(map[string]string{"nofile": "256:" + hard})

	held := readRlimits(map[string]string{"nofile": "256:" + hard})
	if want := "256:" + hard; held["nofile"] != want {
		t.Errorf("readRlimits nofile = %q, want %q", held["nofile"], want)
	}
}

func TestReadRlimitsLeavesOutAnUnknownName(t *testing.T) {
	held := readRlimits(map[string]string{"nofiles": "1048576"})
	if _, present := held["nofiles"]; present {
		t.Errorf("readRlimits = %v, want no entry for nofiles because it is not a resource", held)
	}
}

func TestReadRlimitsReportsNothingForNoLimits(t *testing.T) {
	if held := readRlimits(nil, map[string]string{}); held != nil {
		t.Errorf("readRlimits() = %v, want nil", held)
	}
}

// The defaults and spec.rlimits overlap when the spec overrides a
// default, and the result lists each resource once.
func TestReadRlimitsMergesTheTwoSets(t *testing.T) {
	keepRlimit(t, "nofile")

	held := readRlimits(
		map[string]string{"nofile": "1048576", "nproc": "infinity"},
		map[string]string{"nofile": "256:512"},
	)
	if len(held) != 2 {
		t.Errorf("readRlimits returned %d limits, want 2: %v", len(held), held)
	}
}
