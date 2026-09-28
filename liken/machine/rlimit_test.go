package machine

import (
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestParseRlimitReadsSystemdsThreeForms(t *testing.T) {
	cases := []struct {
		value string
		want  unix.Rlimit
	}{
		{"1048576", unix.Rlimit{Cur: 1048576, Max: 1048576}},
		{"0", unix.Rlimit{Cur: 0, Max: 0}},
		{"infinity", unix.Rlimit{Cur: unix.RLIM_INFINITY, Max: unix.RLIM_INFINITY}},
		{"1024:1048576", unix.Rlimit{Cur: 1024, Max: 1048576}},
		{"1024:infinity", unix.Rlimit{Cur: 1024, Max: unix.RLIM_INFINITY}},
		{"0:infinity", unix.Rlimit{Cur: 0, Max: unix.RLIM_INFINITY}},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			got, err := ParseRlimit(c.value)
			if err != nil {
				t.Fatalf("ParseRlimit(%q): %v", c.value, err)
			}
			if got != c.want {
				t.Errorf("ParseRlimit(%q) = %+v, want %+v", c.value, got, c.want)
			}
		})
	}
}

func TestParseRlimitRefusesValuesThatWouldApplyNothing(t *testing.T) {
	cases := []string{
		"",
		"unlimited",
		"-1",
		"1024:",
		":4096",
		"1e6",
		"1048576:1024",
		"1024:2048:4096",
	}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			if got, err := ParseRlimit(value); err == nil {
				t.Errorf("ParseRlimit(%q) = %+v, want an error", value, got)
			}
		})
	}
}

// A soft limit above the hard limit is the one error the syscall
// would report as a bare EINVAL, so the message must name both halves.
func TestParseRlimitSaysWhichHalfIsWrong(t *testing.T) {
	_, err := ParseRlimit("1048576:1024")
	if err == nil {
		t.Fatal("ParseRlimit accepted a soft limit above the hard limit")
	}
	for _, want := range []string{"1048576", "1024", "hard limit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestFormatRlimitRendersTheSpecsOwnSyntax(t *testing.T) {
	cases := []struct {
		lim  unix.Rlimit
		want string
	}{
		{unix.Rlimit{Cur: 1048576, Max: 1048576}, "1048576"},
		{unix.Rlimit{Cur: unix.RLIM_INFINITY, Max: unix.RLIM_INFINITY}, "infinity"},
		{unix.Rlimit{Cur: 1024, Max: 4096}, "1024:4096"},
		{unix.Rlimit{Cur: 0, Max: unix.RLIM_INFINITY}, "0:infinity"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := FormatRlimit(c.lim); got != c.want {
				t.Errorf("FormatRlimit(%+v) = %q, want %q", c.lim, got, c.want)
			}
		})
	}
}

// The status reports in the spec's grammar, so every value the status
// can print must be a value the spec can declare.
func TestFormatRlimitRoundTripsThroughParseRlimit(t *testing.T) {
	for _, value := range []string{"1048576", "infinity", "1024:4096", "0", "0:infinity"} {
		t.Run(value, func(t *testing.T) {
			lim, err := ParseRlimit(value)
			if err != nil {
				t.Fatalf("ParseRlimit(%q): %v", value, err)
			}
			if got := FormatRlimit(lim); got != value {
				t.Errorf("round trip of %q gave %q", value, got)
			}
		})
	}
}

// keepNofile restores RLIMIT_NOFILE when the test ends. A test may
// lower a soft limit and raise it again, but an unprivileged process
// can never raise a hard limit it lowered, so these tests leave the
// hard limit exactly where they found it.
func keepNofile(t *testing.T) unix.Rlimit {
	t.Helper()
	var saved unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &saved); err != nil {
		t.Fatalf("reading nofile: %v", err)
	}
	if saved.Max != unix.RLIM_INFINITY && saved.Max < 512 {
		t.Skipf("hard nofile limit is %d, too low to test against", saved.Max)
	}
	t.Cleanup(func() {
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &saved); err != nil {
			t.Errorf("restoring nofile: %v", err)
		}
	})
	return saved
}

func TestApplyRlimitSetsTheLimitAndReadRlimitReportsIt(t *testing.T) {
	saved := keepNofile(t)
	want := "256:" + formatRlimitHalf(saved.Max)

	if err := ApplyRlimit("nofile", want); err != nil {
		t.Fatalf("ApplyRlimit: %v", err)
	}

	got, err := ReadRlimit("nofile")
	if err != nil {
		t.Fatalf("ReadRlimit: %v", err)
	}
	if got != want {
		t.Errorf("ReadRlimit = %q, want %q", got, want)
	}
}

// A limit the kernel clamped or refused must show its real value,
// because status.rlimits is the list of limits that hold rather than
// the list somebody asked for.
func TestApplyRlimitRefusesToRaiseAHardLimit(t *testing.T) {
	saved := keepNofile(t)
	if saved.Max == unix.RLIM_INFINITY {
		t.Skip("the hard limit is already unlimited, so nothing can raise it")
	}
	before, err := ReadRlimit("nofile")
	if err != nil {
		t.Fatalf("ReadRlimit: %v", err)
	}

	// Raising a hard limit needs CAP_SYS_RESOURCE, which a test does
	// not have. The refusal must be reported, not swallowed.
	if err := ApplyRlimit("nofile", formatRlimitHalf(saved.Max+1)); err == nil {
		t.Error("ApplyRlimit raised a hard limit without the capability to do it")
	}

	after, err := ReadRlimit("nofile")
	if err != nil {
		t.Fatalf("ReadRlimit: %v", err)
	}
	if after != before {
		t.Errorf("a refused ApplyRlimit changed the limit from %s to %s", before, after)
	}
}

func TestApplyRlimitRefusesAnUnknownResource(t *testing.T) {
	if err := ApplyRlimit("nofiles", "1048576"); err == nil {
		t.Error("ApplyRlimit accepted a resource the kernel does not have")
	}
}

func TestApplyRlimitRefusesAValueThatWillNotParse(t *testing.T) {
	if err := ApplyRlimit("nofile", "lots"); err == nil {
		t.Error("ApplyRlimit accepted a value that is not a number")
	}
}

func TestReadRlimitRefusesAnUnknownResource(t *testing.T) {
	if _, err := ReadRlimit("nofiles"); err == nil {
		t.Error("ReadRlimit accepted a resource the kernel does not have")
	}
}

func TestRlimitResourceRefusesAnUnknownName(t *testing.T) {
	_, err := RlimitResource("nofiles")
	if err == nil {
		t.Fatal("RlimitResource accepted a name the kernel has no limit for")
	}
	// The message must list what is accepted, because the failure a
	// person hits here is a typo and the fix is the correct spelling.
	if !strings.Contains(err.Error(), "nofile") {
		t.Errorf("error %q does not list the accepted names", err)
	}
}

// OSRlimits ships with the release, so a bad entry would reach a whole
// fleet at once. Every entry must name a real resource and parse.
func TestOSRlimitsAreAllApplicable(t *testing.T) {
	for name, value := range OSRlimits {
		t.Run(name, func(t *testing.T) {
			if _, err := RlimitResource(name); err != nil {
				t.Errorf("OSRlimits names %s: %v", name, err)
			}
			if _, err := ParseRlimit(value); err != nil {
				t.Errorf("OSRlimits sets %s = %s: %v", name, value, err)
			}
		})
	}
}

// The three values k3s's own systemd unit sets, and liken's standing
// on each. A change to any of these is a change to the parity claim
// that rlimitdefaults.go makes, so it should have to change this test.
func TestOSRlimitsMatchK3sUnitWhereLikenClaimsParity(t *testing.T) {
	if got := OSRlimits["nofile"]; got != "1048576" {
		t.Errorf("nofile = %q, want 1048576, which is LimitNOFILE in k3s's unit", got)
	}
	if got := OSRlimits["nproc"]; got != "infinity" {
		t.Errorf("nproc = %q, want infinity, which is LimitNPROC in k3s's unit", got)
	}
	// LimitCORE=infinity is the deliberate deviation. liken has no
	// collector to bound a dump, so the soft limit stays at the
	// kernel's 0 and a crashing container writes no core beside etcd.
	if got, ok := OSRlimits["core"]; ok {
		t.Errorf("core = %q, want no entry; rlimitdefaults.go explains the deviation", got)
	}
}

// nofile cannot exceed fs.nr_open, which the kernel defaults to
// 1048576. A larger value would fail to apply rather than clamp, and
// liken sets no sysctl that raises nr_open.
func TestOSRlimitsNofileFitsUnderTheKernelsNrOpenDefault(t *testing.T) {
	lim, err := ParseRlimit(OSRlimits["nofile"])
	if err != nil {
		t.Fatalf("parsing the nofile default: %v", err)
	}
	const nrOpenDefault = 1048576
	if lim.Max > nrOpenDefault {
		t.Errorf("nofile hard limit %d is above the kernel's fs.nr_open default of %d",
			lim.Max, nrOpenDefault)
	}
	if _, raises := OSSysctls["fs.nr_open"]; raises {
		t.Error("OSSysctls now raises fs.nr_open; this test's ceiling is stale")
	}
}
