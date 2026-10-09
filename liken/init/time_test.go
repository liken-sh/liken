package main

// Tests for the clock discipline's decisions: where a machine gets
// its time, what it reports about its clock, and how much slewing it
// asks of the kernel at once. The syscalls that act on these
// decisions, clock_settime and adjtimex, run only as PID 1, so the
// boot step's tests replace them with `fakeClockActions`. Tests for
// the syscalls themselves belong to the QEMU harness.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

func clusterWithTime(upstreams []string, endpoint string, leaders ...string) *cluster.Cluster {
	if leaders == nil {
		leaders = []string{"node-1"}
	}
	return &cluster.Cluster{
		Spec: cluster.ClusterSpec{
			Leaders:  leaders,
			Endpoint: endpoint,
			Network:  cluster.ClusterNetworkSpec{NodeCIDR: "10.10.0.0/24"},
			Time:     cluster.ClusterTimeSpec{Upstreams: upstreams},
		},
	}
}

// manifestsDir builds a machines directory in the same form the
// image uses: one Machine manifest per name, each declaring a static
// address on the node network.
func manifestsDir(t *testing.T, addresses map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, address := range addresses {
		doc := `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: ` + name + `
spec:
  network:
    interfaces:
      - name: eth1
        address: ` + address + `
`
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTimeSourcesLeaderAsksTheUpstreams(t *testing.T) {
	c := clusterWithTime([]string{"time.cloudflare.com", "192.168.1.1"}, "https://10.10.0.1:6443")
	sources := timeSources(c, api.RoleLeader, t.TempDir())
	if len(sources) != 2 || sources[0] != "time.cloudflare.com" || sources[1] != "192.168.1.1" {
		t.Errorf("got %v", sources)
	}
}

func TestTimeSourcesLeaderWithoutUpstreamsFreeRuns(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	if sources := timeSources(c, api.RoleLeader, t.TempDir()); sources != nil {
		t.Errorf("expected free-running, got %v", sources)
	}
}

func TestTimeSourcesNilClusterFreeRuns(t *testing.T) {
	if sources := timeSources(nil, api.RoleLeader, t.TempDir()); sources != nil {
		t.Errorf("expected free-running, got %v", sources)
	}
}

func TestTimeSourcesFollowerAsksEveryLeader(t *testing.T) {
	c := clusterWithTime(nil, "https://cluster.example.com:6443", "node-1", "node-3")
	dir := manifestsDir(t, map[string]string{
		"node-1": "10.10.0.1/24",
		"node-2": "10.10.0.2/24", // a follower's manifest: present in the image, not a time source
		"node-3": "10.10.0.3/24",
	})
	sources := timeSources(c, api.RoleFollower, dir)
	want := []string{"10.10.0.1", "10.10.0.3", "cluster.example.com"}
	if !slices.Equal(sources, want) {
		t.Errorf("got %v, want %v", sources, want)
	}
}

func TestTimeSourcesFollowerDoesNotAskTheEndpointTwice(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	dir := manifestsDir(t, map[string]string{"node-1": "10.10.0.1/24"})
	sources := timeSources(c, api.RoleFollower, dir)
	if !slices.Equal(sources, []string{"10.10.0.1"}) {
		t.Errorf("got %v", sources)
	}
}

func TestTimeSourcesFollowerFallsBackToTheEndpoint(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	sources := timeSources(c, api.RoleFollower, t.TempDir())
	if !slices.Equal(sources, []string{"10.10.0.1"}) {
		t.Errorf("got %v", sources)
	}
}

// A leader that cannot be resolved leaves the follower asking the
// endpoint's host. A typo in `nodeCIDR` or a damaged manifest must not
// leave a follower with no time source.
func TestTimeSourcesFollowerFallsBackPastAnUnresolvableLeader(t *testing.T) {
	cases := []struct {
		name     string
		nodeCIDR string
		manifest string
	}{
		{"a node network that does not parse", "10.10.0.0/99", "kind: Machine\nmetadata:\n  name: node-1\n"},
		{"a leader manifest that does not parse", "10.10.0.0/24", "kind: Machine\nmetadata: [node-1\n"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			c := clusterWithTime(nil, "https://cluster.example.com:6443")
			c.Spec.Network.NodeCIDR = one.nodeCIDR
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "node-1.yaml"), []byte(one.manifest), 0o644); err != nil {
				t.Fatal(err)
			}

			sources := timeSources(c, api.RoleFollower, dir)

			if !slices.Equal(sources, []string{"cluster.example.com"}) {
				t.Errorf("got %v", sources)
			}
		})
	}
}

func TestTimeStatusAfterASync(t *testing.T) {
	at := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	sync := &timeSync{
		source:  "time.cloudflare.com",
		stratum: 2,
		offset:  1280 * time.Microsecond,
		at:      at,
	}
	status := timeStatus(sync, []string{"time.cloudflare.com"})
	if status.State != machine.TimeSynchronized {
		t.Errorf("a fresh sync should report Synchronized, got %q", status.State)
	}
	if status.Source != "time.cloudflare.com" {
		t.Errorf("source: got %q", status.Source)
	}
	if status.Stratum != 3 {
		t.Errorf("a stratum-2 source makes this machine stratum 3, got %d", status.Stratum)
	}
	if status.Offset != "1.28ms" {
		t.Errorf("offset: got %q", status.Offset)
	}
	if status.LastSync == nil || !status.LastSync.Equal(at) {
		t.Errorf("lastSync: got %v", status.LastSync)
	}
}

func TestTimeStatusFreeRunning(t *testing.T) {
	status := timeStatus(nil, nil)
	if status.State != machine.TimeFreeRunning {
		t.Errorf("no sources means free-running by design, got %q", status.State)
	}
	if status.Stratum != 10 {
		t.Errorf("free-running reports the local-clock convention, got %d", status.Stratum)
	}
}

func TestTimeStatusNeverSynced(t *testing.T) {
	status := timeStatus(nil, []string{"10.10.0.1"})
	if status.State != machine.TimeUnsynchronized {
		t.Errorf("sources declared but never reached is an outage, got %q", status.State)
	}
	if status.Stratum != 16 {
		t.Errorf("unsynchronized reports stratum 16, got %d", status.Stratum)
	}
}

func synchronizedStatus(source string, stratum int) machine.TimeStatus {
	return machine.TimeStatus{State: machine.TimeSynchronized, Source: source, Stratum: stratum}
}

func TestWorthRepublishingOnAnyStateSourceOrStratumChange(t *testing.T) {
	published := synchronizedStatus("10.10.0.1", 3)
	if worthRepublishing(published, machine.TimeStatus{State: machine.TimeUnsynchronized}, 0, time.Minute) != true {
		t.Error("losing sync is always news")
	}
	if worthRepublishing(published, synchronizedStatus("10.10.0.2", 3), 0, time.Minute) != true {
		t.Error("a different source is news")
	}
	if worthRepublishing(published, synchronizedStatus("10.10.0.1", 4), 0, time.Minute) != true {
		t.Error("a different stratum is news")
	}
}

func TestWorthRepublishingIgnoresJitter(t *testing.T) {
	published := synchronizedStatus("10.10.0.1", 3)
	if worthRepublishing(published, published, 800*time.Microsecond, time.Minute) {
		t.Error("sub-threshold wobble is not news; every republish is an etcd write on every machine")
	}
}

func TestWorthRepublishingReportsRealDrift(t *testing.T) {
	published := synchronizedStatus("10.10.0.1", 3)
	if !worthRepublishing(published, published, 30*time.Millisecond, time.Minute) {
		t.Error("a clock that moved past the threshold is news")
	}
	if !worthRepublishing(published, published, -30*time.Millisecond, time.Minute) {
		t.Error("drift is news in either direction")
	}
}

func TestWorthRepublishingBoundsLastSyncStaleness(t *testing.T) {
	published := synchronizedStatus("10.10.0.1", 3)
	if !worthRepublishing(published, published, 0, 11*time.Minute) {
		t.Error("the freshness floor keeps lastSync from lying about a healthy sync loop")
	}
}

func TestSlewAmountPassesSmallOffsetsThrough(t *testing.T) {
	if got := slewAmount(3 * time.Millisecond); got != 3*time.Millisecond {
		t.Errorf("got %v", got)
	}
	if got := slewAmount(-3 * time.Millisecond); got != -3*time.Millisecond {
		t.Errorf("got %v", got)
	}
}

func TestSlewAmountClampsLargeOffsets(t *testing.T) {
	if got := slewAmount(3 * time.Second); got != 500*time.Millisecond {
		t.Errorf("got %v", got)
	}
	if got := slewAmount(-3 * time.Second); got != -500*time.Millisecond {
		t.Errorf("got %v", got)
	}
}

func TestStepClockAtBootWithNoSourcesFreeRuns(t *testing.T) {
	if sync := stepClockAtBoot(nil); sync != nil {
		t.Errorf("no sources means no measurement, not a fake one: %+v", sync)
	}
}

func TestQuerySourcesTakesTheFirstAnswer(t *testing.T) {
	addr := startResponder(t, syncedClock())
	sync, err := querySources([]string{addr})
	if err != nil {
		t.Fatal(err)
	}
	if sync.source != addr || sync.stratum != 4 {
		t.Errorf("the answer names its source and stratum: %+v", sync)
	}
	if sync.offset.Abs() > 250*time.Millisecond {
		t.Errorf("a loopback measurement is near zero: %v", sync.offset)
	}
}

func TestQuerySourcesReportsTheLastFailure(t *testing.T) {
	if _, err := querySources([]string{"not::a::valid::address::"}); err == nil {
		t.Error("an unreachable source is an error, never a fake measurement")
	}
}

func TestStepClockAtBootSlewsWhenTheClockIsClose(t *testing.T) {
	// The responder answers using this same machine's clock, so the
	// measured offset is only microseconds, far under the step
	// threshold. This means stepClockAtBoot returns the measurement
	// without calling clock_settime. TestStepClockAtBootStepsAWrongClock
	// covers the step against a fake clock. A real clock_settime
	// needs the QEMU harness's -rtc drills.
	addr := startResponder(t, syncedClock())
	sync := stepClockAtBoot([]string{addr})
	if sync == nil || sync.source != addr {
		t.Fatalf("the first sync comes back for status: %+v", sync)
	}
}

// pollAnswer is one scripted poll of the time sources: the offset a
// source measured, or no answer at all.
type pollAnswer struct {
	offset   time.Duration
	answered bool
}

func timeAnswer(offset time.Duration) pollAnswer { return pollAnswer{offset: offset, answered: true} }

var noTimeAnswer = pollAnswer{}

var errNoTimeAnswer = errors.New("no time source answered")

// fakeClockActions stands in for the network query and the clock
// syscalls. Each query takes the next scripted answer, and a script
// that has run out answers nothing. The fake records each step, slew,
// and RTC write, so a test checks what the machine did to its clocks.
type fakeClockActions struct {
	mu        sync.Mutex
	polls     []pollAnswer
	queries   int
	steps     []time.Time
	slews     []time.Duration
	rtcWrites int
	stepErr   error
	slewErr   error
}

func installFakeClock(t *testing.T, polls ...pollAnswer) *fakeClockActions {
	t.Helper()
	f := &fakeClockActions{polls: polls}
	savedQuery, savedSet, savedSlew, savedRTC := queryTimeSources, setSystemClock, slewSystemClock, saveHardwareClock
	t.Cleanup(func() {
		queryTimeSources, setSystemClock, slewSystemClock, saveHardwareClock = savedQuery, savedSet, savedSlew, savedRTC
	})
	queryTimeSources, setSystemClock, slewSystemClock, saveHardwareClock = f.query, f.step, f.slew, f.writeRTC
	return f
}

func (f *fakeClockActions) query(sources []string) (*timeSync, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries++
	next := noTimeAnswer
	if len(f.polls) > 0 {
		next, f.polls = f.polls[0], f.polls[1:]
	}
	if !next.answered {
		return nil, errNoTimeAnswer
	}
	return &timeSync{source: sources[0], stratum: 2, offset: next.offset, at: time.Now()}, nil
}

func (f *fakeClockActions) step(to time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, to)
	return f.stepErr
}

func (f *fakeClockActions) slew(offset time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slews = append(f.slews, offset)
	return f.slewErr
}

func (f *fakeClockActions) writeRTC() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rtcWrites++
}

func (f *fakeClockActions) recorded() (queries int, steps []time.Time, slews []time.Duration, rtcWrites int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries, slices.Clone(f.steps), slices.Clone(f.slews), f.rtcWrites
}

// A boot whose clock is 2 seconds slow steps the clock forward by
// exactly the measured offset before k3s starts. A smaller correction
// would leave certificates from the cluster's CA in the future.
func TestStepClockAtBootStepsAWrongClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(2*time.Second))
		start := time.Now()

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		_, steps, _, _ := f.recorded()
		if !slices.Equal(steps, []time.Time{start.Add(2 * time.Second)}) {
			t.Errorf("the clock steps once, to the measured time: %v", steps)
		}
		if sync == nil || sync.offset != 2*time.Second || sync.source != "10.10.0.1" {
			t.Errorf("the measurement comes back for status: %+v", sync)
		}
	})
}

// A refused step does not stop the boot. The measurement still comes
// back, so status reports what the source said, and the discipline
// loop slews the clock toward it.
func TestStepClockAtBootKeepsTheMeasurementWhenTheStepFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(-5*time.Second))
		f.stepErr = unix.EPERM

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		if sync == nil || sync.offset != -5*time.Second {
			t.Errorf("a failed step still reports the measurement: %+v", sync)
		}
	})
}

// A boot asks its sources three times over 6 seconds and then boots
// on the hardware clock. A machine's boot must not wait without limit
// for a network that may never answer.
func TestStepClockAtBootGivesUpAfterThreeAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t)
		start := time.Now()

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		queries, steps, _, _ := f.recorded()
		if sync != nil || len(steps) != 0 {
			t.Errorf("no answer means no measurement and no step: %+v %v", sync, steps)
		}
		if queries != 3 {
			t.Errorf("the boot asks 3 times, got %d", queries)
		}
		if waited := time.Since(start); waited != 6*time.Second {
			t.Errorf("the boot waits 1s, 2s, and 3s between attempts, got %v", waited)
		}
	})
}

// A source that answers on the second attempt still sets the boot's
// time. A close clock is left for the discipline loop to slew, and is
// never stepped.
func TestStepClockAtBootTakesALateAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, noTimeAnswer, timeAnswer(10*time.Millisecond))
		start := time.Now()

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		_, steps, _, _ := f.recorded()
		if sync == nil || sync.offset != 10*time.Millisecond {
			t.Errorf("the second attempt's answer comes back: %+v", sync)
		}
		if len(steps) != 0 {
			t.Errorf("an offset under the step threshold is slewed, not stepped: %v", steps)
		}
		if waited := time.Since(start); waited != time.Second {
			t.Errorf("one failed attempt costs one second, got %v", waited)
		}
	})
}
