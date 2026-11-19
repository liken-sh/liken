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
				t.Fatalf("parsing %q: %v", c.value, err)
			}
			if got != c.want {
				t.Errorf("ParseRlimit(%q) is %+v, want %+v", c.value, got, c.want)
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
				t.Errorf("ParseRlimit(%q) is %+v, want an error", value, got)
			}
		})
	}
}

// The syscall reports a soft limit above the hard limit as a bare
// EINVAL, so the error message must show both values.
func TestParseRlimitErrorShowsBothLimitsWhenSoftIsAboveHard(t *testing.T) {
	_, err := ParseRlimit("1048576:1024")
	if err == nil {
		t.Fatal("ParseRlimit accepted a soft limit above the hard limit, want an error")
	}
	for _, want := range []string{"1048576", "1024", "hard limit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to mention %q", err, want)
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
				t.Errorf("FormatRlimit(%+v) is %q, want %q", c.lim, got, c.want)
			}
		})
	}
}

// Every value that the status can report must be valid in a spec.
func TestFormatRlimitRoundTripsThroughParseRlimit(t *testing.T) {
	for _, value := range []string{"1048576", "infinity", "1024:4096", "0", "0:infinity"} {
		t.Run(value, func(t *testing.T) {
			lim, err := ParseRlimit(value)
			if err != nil {
				t.Fatalf("parsing %q: %v", value, err)
			}
			if got := FormatRlimit(lim); got != value {
				t.Errorf("the round trip of %q gives %q, want the same value", value, got)
			}
		})
	}
}

// keepNofile restores RLIMIT_NOFILE when the test ends. An
// unprivileged process cannot raise a hard limit that it lowered, so
// the tests change only the soft limit.
func keepNofile(t *testing.T) unix.Rlimit {
	t.Helper()
	var saved unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &saved); err != nil {
		t.Fatalf("reading the nofile limit: %v", err)
	}
	if saved.Max != unix.RLIM_INFINITY && saved.Max < 512 {
		t.Skipf("hard nofile limit is %d, too low to test against", saved.Max)
	}
	t.Cleanup(func() {
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &saved); err != nil {
			t.Errorf("restoring the nofile limit: %v", err)
		}
	})
	return saved
}

func TestApplyRlimitSetsTheLimitAndReadRlimitReportsIt(t *testing.T) {
	saved := keepNofile(t)
	want := "256:" + formatRlimitHalf(saved.Max)

	if err := ApplyRlimit("nofile", want); err != nil {
		t.Fatalf("applying the limit: %v", err)
	}

	got, err := ReadRlimit("nofile")
	if err != nil {
		t.Fatalf("reading the limit: %v", err)
	}
	if got != want {
		t.Errorf("ReadRlimit is %q, want %q", got, want)
	}
}

// status.rlimits reports what ReadRlimit reads, so a refused change
// must leave the value that it reads unchanged.
func TestApplyRlimitRefusesToRaiseAHardLimit(t *testing.T) {
	saved := keepNofile(t)
	if saved.Max == unix.RLIM_INFINITY {
		t.Skip("the hard limit is already unlimited, so nothing can raise it")
	}
	before, err := ReadRlimit("nofile")
	if err != nil {
		t.Fatalf("reading the limit: %v", err)
	}

	// Raising a hard limit needs CAP_SYS_RESOURCE, which the test does
	// not have.
	if err := ApplyRlimit("nofile", formatRlimitHalf(saved.Max+1)); err == nil {
		t.Error("ApplyRlimit raised a hard limit without CAP_SYS_RESOURCE, want an error")
	}

	after, err := ReadRlimit("nofile")
	if err != nil {
		t.Fatalf("reading the limit: %v", err)
	}
	if after != before {
		t.Errorf("the limit is %s after a refused ApplyRlimit, want %s", after, before)
	}
}

func TestApplyRlimitRefusesAnUnknownResource(t *testing.T) {
	if err := ApplyRlimit("nofiles", "1048576"); err == nil {
		t.Error("ApplyRlimit accepted a resource the kernel does not have, want an error")
	}
}

func TestApplyRlimitRefusesAValueThatWillNotParse(t *testing.T) {
	if err := ApplyRlimit("nofile", "lots"); err == nil {
		t.Error("ApplyRlimit accepted a value that is not a number, want an error")
	}
}

func TestReadRlimitRefusesAnUnknownResource(t *testing.T) {
	if _, err := ReadRlimit("nofiles"); err == nil {
		t.Error("ReadRlimit accepted a resource the kernel does not have, want an error")
	}
}

func TestRlimitResourceRefusesAnUnknownName(t *testing.T) {
	_, err := RlimitResource("nofiles")
	if err == nil {
		t.Fatal("RlimitResource accepted a name the kernel has no limit for, want an error")
	}
	// The usual cause is a typo, so the message lists the valid names.
	if !strings.Contains(err.Error(), "nofile") {
		t.Errorf("the error is %q, want it to list the accepted names", err)
	}
}

// OSRlimits ships with every release, so each entry must name a known
// resource and have a value that parses.
func TestOSRlimitsAreAllApplicable(t *testing.T) {
	for name, value := range OSRlimits {
		t.Run(name, func(t *testing.T) {
			if _, err := RlimitResource(name); err != nil {
				t.Errorf("OSRlimits has the resource %s: %v", name, err)
			}
			if _, err := ParseRlimit(value); err != nil {
				t.Errorf("OSRlimits has the value %s = %s: %v", name, value, err)
			}
		})
	}
}

// OSRlimits matches the limits in k3s's own systemd unit, except for
// core. A change here changes the claim that rlimitdefaults.go makes,
// so this test must change with it.
func TestOSRlimitsMatchTheLimitsOfTheK3sUnit(t *testing.T) {
	if got := OSRlimits["nofile"]; got != "1048576" {
		t.Errorf("nofile is %q, want 1048576, which is LimitNOFILE in k3s's unit", got)
	}
	if got := OSRlimits["nproc"]; got != "infinity" {
		t.Errorf("nproc is %q, want infinity, which is LimitNPROC in k3s's unit", got)
	}
	// The k3s unit sets LimitCORE=infinity. liken keeps the kernel's
	// soft core limit of 0, because nothing on the machine collects or
	// bounds core dumps, and a dump would fill the filesystem that
	// holds etcd.
	if got, ok := OSRlimits["core"]; ok {
		t.Errorf("core is %q, want no entry, as rlimitdefaults.go explains", got)
	}
}

// The kernel refuses a nofile limit above fs.nr_open, which defaults
// to 1048576, and does not clamp it. liken sets no sysctl that raises
// nr_open, so the default nofile must not exceed that value.
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
		t.Error("OSSysctls sets fs.nr_open, so the ceiling of this test is out of date")
	}
}
