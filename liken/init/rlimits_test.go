package main

import (
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// The property that matters is not what init holds. It is what a child
// inherits, because init starts k3s and k3s starts everything else.
//
// The two can differ. Go's runtime raises its own soft RLIMIT_NOFILE
// to just under the hard limit at startup and remembers the original,
// and os/exec restores that original in every child it starts, so that
// a program using select(2) is not handed a descriptor it cannot
// represent. A limit applied through the wrong call leaves that memory
// in place: /proc/1/limits would show the raised ceiling while every
// process below init kept the old one. These tests are what stands
// between liken and that failure, so they read a child's limits and
// never init's own.
//
// The child must not be a Go program. A Go child runs the same startup
// adjustment and reports its own raised soft limit rather than the one
// it inherited, which is the same reason an untuned liken machine
// reads 4095 instead of 1024. So the child here is cat, reading
// /proc/self/limits, which is also the file an operator reads to
// confirm this on a real machine.

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
			t.Fatalf("cannot read %q from %q", row, line)
		}
		return fields[0], fields[1]
	}
	t.Fatalf("no %q row in the child's limits", row)
	return "", ""
}

// keepRlimit restores a limit when the test ends, so one test's
// setting cannot leak into another's. A test may lower a soft limit
// and raise it again, but an unprivileged process can never raise a
// hard limit it lowered. So every test here leaves the hard limit
// where it found it, and this helper's restore is guaranteed to work.
func keepRlimit(t *testing.T, resource string) unix.Rlimit {
	t.Helper()
	n, err := machine.RlimitResource(resource)
	if err != nil {
		t.Fatalf("resolving %s: %v", resource, err)
	}
	var saved unix.Rlimit
	if err := unix.Getrlimit(n, &saved); err != nil {
		t.Fatalf("reading %s: %v", resource, err)
	}
	t.Cleanup(func() {
		if err := unix.Setrlimit(n, &saved); err != nil {
			t.Errorf("restoring %s: %v", resource, err)
		}
	})
	return saved
}

// hardNofile returns this test process's hard limit as a string, so a
// test can set a soft limit against it without ever lowering it.
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
		t.Errorf("a child inherited a soft nofile of %s, want 256; "+
			"the limit did not survive exec, so k3s would not have received it", gotSoft)
	}
	if gotHard != hard {
		t.Errorf("a child inherited a hard nofile of %s, want %s", gotHard, hard)
	}
}

// The hard limit is the half that failed in the field: ClickHouse
// raised its own soft limit to the hard limit and still ran out.
func TestAChildInheritsAHardLimitEqualToItsSoftLimit(t *testing.T) {
	_, hard := hardNofile(t)

	applyRlimits(map[string]string{"nofile": hard})

	gotSoft, gotHard := childLimits(t, "Max open files")
	if gotSoft != hard || gotHard != hard {
		t.Errorf("a child inherited nofile %s:%s, want %s:%s", gotSoft, gotHard, hard, hard)
	}
}

// A misspelled resource must not stop the boot, and it must not stop
// the entries beside it from applying.
func TestApplyRlimitsSkipsABadEntryAndKeepsGoing(t *testing.T) {
	_, hard := hardNofile(t)

	applyRlimits(map[string]string{
		"nofiles": "1048576", // misspelled: no such resource
		"nofile":  "256:" + hard,
		"nproc":   "not a number",
	})

	if gotSoft, _ := childLimits(t, "Max open files"); gotSoft != "256" {
		t.Errorf("soft nofile = %s, want 256; a bad entry beside it stopped it applying", gotSoft)
	}
}

func TestReadRlimitsReportsWhatTheKernelHolds(t *testing.T) {
	_, hard := hardNofile(t)

	applyRlimits(map[string]string{"nofile": "256:" + hard})

	held := readRlimits(map[string]string{"nofile": "256:" + hard})
	if want := "256:" + hard; held["nofile"] != want {
		t.Errorf("readRlimits reported nofile %q, want %q", held["nofile"], want)
	}
}

// A name that never applied is absent from the report, so the map is
// the list of limits that hold rather than the list somebody wanted.
func TestReadRlimitsLeavesOutAnUnknownName(t *testing.T) {
	held := readRlimits(map[string]string{"nofiles": "1048576"})
	if _, present := held["nofiles"]; present {
		t.Errorf("readRlimits reported a name the kernel has no limit for: %v", held)
	}
}

func TestReadRlimitsReportsNothingForNoLimits(t *testing.T) {
	if held := readRlimits(nil, map[string]string{}); held != nil {
		t.Errorf("readRlimits() = %v, want nil", held)
	}
}

// The two sets init passes overlap, because a spec overrides an entry
// in the defaults table. The report names each resource once.
func TestReadRlimitsMergesTheTwoSets(t *testing.T) {
	keepRlimit(t, "nofile")

	held := readRlimits(
		map[string]string{"nofile": "1048576", "nproc": "infinity"},
		map[string]string{"nofile": "256:512"},
	)
	if len(held) != 2 {
		t.Errorf("readRlimits reported %d limits, want 2: %v", len(held), held)
	}
}
