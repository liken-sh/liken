package main

// The syscalls clock_settime and adjtimex need PID 1's privileges, so
// these tests replace them with fakeClockActions. The QEMU harness
// tests the real syscalls.

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

// manifestsDir writes a directory like the image's machines directory:
// one Machine manifest per name, each with a static address on the
// node network.
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
			t.Fatalf("writing the manifest of %s: %v", name, err)
		}
	}
	return dir
}

func TestTimeSourcesLeaderUsesTheUpstreams(t *testing.T) {
	c := clusterWithTime([]string{"time.cloudflare.com", "192.168.1.1"}, "https://10.10.0.1:6443")
	sources := timeSources(c, api.RoleLeader, t.TempDir())
	if len(sources) != 2 || sources[0] != "time.cloudflare.com" || sources[1] != "192.168.1.1" {
		t.Errorf("sources are %v, want [time.cloudflare.com 192.168.1.1]", sources)
	}
}

func TestTimeSourcesLeaderWithoutUpstreamsFreeRuns(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	if sources := timeSources(c, api.RoleLeader, t.TempDir()); sources != nil {
		t.Errorf("sources are %v, want none (free-running)", sources)
	}
}

func TestTimeSourcesNilClusterFreeRuns(t *testing.T) {
	if sources := timeSources(nil, api.RoleLeader, t.TempDir()); sources != nil {
		t.Errorf("sources are %v, want none (free-running)", sources)
	}
}

func TestTimeSourcesFollowerUsesEveryLeader(t *testing.T) {
	c := clusterWithTime(nil, "https://cluster.example.com:6443", "node-1", "node-3")
	dir := manifestsDir(t, map[string]string{
		"node-1": "10.10.0.1/24",
		"node-2": "10.10.0.2/24", // a follower's manifest: present in the image, not a time source
		"node-3": "10.10.0.3/24",
	})
	sources := timeSources(c, api.RoleFollower, dir)
	want := []string{"10.10.0.1", "10.10.0.3", "cluster.example.com"}
	if !slices.Equal(sources, want) {
		t.Errorf("sources are %v, want %v", sources, want)
	}
}

func TestTimeSourcesFollowerListsTheEndpointOnce(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	dir := manifestsDir(t, map[string]string{"node-1": "10.10.0.1/24"})
	sources := timeSources(c, api.RoleFollower, dir)
	if !slices.Equal(sources, []string{"10.10.0.1"}) {
		t.Errorf("sources are %v, want [10.10.0.1]", sources)
	}
}

func TestTimeSourcesFollowerFallsBackToTheEndpoint(t *testing.T) {
	c := clusterWithTime(nil, "https://10.10.0.1:6443")
	sources := timeSources(c, api.RoleFollower, t.TempDir())
	if !slices.Equal(sources, []string{"10.10.0.1"}) {
		t.Errorf("sources are %v, want [10.10.0.1]", sources)
	}
}

// When a leader's address cannot be resolved, the follower queries the
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
				t.Fatalf("writing node-1.yaml: %v", err)
			}

			sources := timeSources(c, api.RoleFollower, dir)

			if !slices.Equal(sources, []string{"cluster.example.com"}) {
				t.Errorf("sources are %v, want [cluster.example.com]", sources)
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
		t.Errorf("the state is %q, want Synchronized", status.State)
	}
	if status.Source != "time.cloudflare.com" {
		t.Errorf("the source is %q, want time.cloudflare.com", status.Source)
	}
	if status.Stratum != 3 {
		t.Errorf("the stratum is %d, want 3 (one more than the source)", status.Stratum)
	}
	if status.Offset != "±5ms" {
		t.Errorf("the offset is %q, want the bound ±5ms", status.Offset)
	}
}

// A boot that stepped the clock measured the offset before the step, so
// it says nothing about the clock now. Status has no bound until the
// first poll.
func TestTimeStatusAfterAStepHasNoBound(t *testing.T) {
	sync := &timeSync{source: "time.cloudflare.com", stratum: 2, offset: 2 * time.Second, stepped: true}
	if got := timeStatus(sync, []string{"time.cloudflare.com"}).Offset; got != "" {
		t.Errorf("the offset is %q, want none after a step", got)
	}
}

func TestTimeStatusFreeRunning(t *testing.T) {
	status := timeStatus(nil, nil)
	if status.State != machine.TimeFreeRunning {
		t.Errorf("the state is %q, want FreeRunning", status.State)
	}
	if status.Stratum != 10 {
		t.Errorf("the stratum is %d, want 10", status.Stratum)
	}
}

func TestTimeStatusNeverSynced(t *testing.T) {
	status := timeStatus(nil, []string{"10.10.0.1"})
	if status.State != machine.TimeUnsynchronized {
		t.Errorf("the state is %q, want Unsynchronized", status.State)
	}
	if status.Stratum != 16 {
		t.Errorf("the stratum is %d, want 16", status.Stratum)
	}
}

func TestSlewAmountPassesSmallOffsetsThrough(t *testing.T) {
	if got := slewAmount(3 * time.Millisecond); got != 3*time.Millisecond {
		t.Errorf("the slew of 3ms is %v, want 3ms", got)
	}
	if got := slewAmount(-3 * time.Millisecond); got != -3*time.Millisecond {
		t.Errorf("the slew of -3ms is %v, want -3ms", got)
	}
}

func TestSlewAmountClampsLargeOffsets(t *testing.T) {
	if got := slewAmount(3 * time.Second); got != 500*time.Millisecond {
		t.Errorf("the slew of 3s is %v, want 500ms", got)
	}
	if got := slewAmount(-3 * time.Second); got != -500*time.Millisecond {
		t.Errorf("the slew of -3s is %v, want -500ms", got)
	}
}

func TestStepClockAtBootWithNoSourcesFreeRuns(t *testing.T) {
	if sync := stepClockAtBoot(nil); sync != nil {
		t.Errorf("the measurement is %+v, want nil", sync)
	}
}

func TestQuerySourcesReturnsTheFirstAnswer(t *testing.T) {
	addr := startResponder(t, syncedClock())
	sync, err := querySources([]string{addr})
	if err != nil {
		t.Fatalf("querying the responder at %s: %v", addr, err)
	}
	if sync.source != addr || sync.stratum != 4 {
		t.Errorf("the measurement is %+v, want source %s and stratum 4", sync, addr)
	}
	if sync.offset.Abs() > 250*time.Millisecond {
		t.Errorf("the offset is %v, want under 250ms on loopback", sync.offset)
	}
}

func TestQuerySourcesReportsTheLastFailure(t *testing.T) {
	if _, err := querySources([]string{"not::a::valid::address::"}); err == nil {
		t.Error("querySources returned no error for an invalid address, want one")
	}
}

func TestStepClockAtBootSlewsWhenTheClockIsClose(t *testing.T) {
	// The responder answers from this machine's own clock, so the
	// offset is under stepThreshold and stepClockAtBoot does not call
	// clock_settime. TestStepClockAtBootStepsAWrongClock tests the step.
	addr := startResponder(t, syncedClock())
	sync := stepClockAtBoot([]string{addr})
	if sync == nil || sync.source != addr {
		t.Fatalf("the measurement is %+v, want one from source %s", sync, addr)
	}
}

// pollAnswer is one scripted poll: the offset and round trip that a
// source measured, or no answer.
type pollAnswer struct {
	offset   time.Duration
	delay    time.Duration
	answered bool
}

// timeAnswer is an answer with a round trip of 1ms.
func timeAnswer(offset time.Duration) pollAnswer {
	return pollAnswer{offset: offset, delay: time.Millisecond, answered: true}
}

// timeAnswerOver is an answer with the given round trip.
func timeAnswerOver(offset, delay time.Duration) pollAnswer {
	return pollAnswer{offset: offset, delay: delay, answered: true}
}

var noTimeAnswer = pollAnswer{}

var errNoTimeAnswer = errors.New("no time source answered")

// fakeClockActions replaces the network query and the clock syscalls.
// Each query returns the next scripted answer, then no answer when the
// script is used up. It records each step, slew, and RTC write.
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
	savedQuery, savedSet, savedSlew, savedPending, savedRTC := queryTimeSources, setSystemClock, slewSystemClock, pendingSlew, saveHardwareClock
	t.Cleanup(func() {
		queryTimeSources, setSystemClock, slewSystemClock, pendingSlew, saveHardwareClock = savedQuery, savedSet, savedSlew, savedPending, savedRTC
	})
	queryTimeSources, setSystemClock, slewSystemClock, saveHardwareClock = f.query, f.step, f.slew, f.writeRTC
	// The fake kernel applies each slew at once, so nothing is pending.
	pendingSlew = func() time.Duration { return 0 }
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
	return &timeSync{source: sources[0], stratum: 2, offset: next.offset, delay: next.delay, at: time.Now()}, nil
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

// A boot whose clock is 2 seconds slow steps the clock forward by the
// measured offset.
func TestStepClockAtBootStepsAWrongClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(2*time.Second))
		start := time.Now()

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		_, steps, _, _ := f.recorded()
		if !slices.Equal(steps, []time.Time{start.Add(2 * time.Second)}) {
			t.Errorf("the clock steps are %v, want one step to %v", steps, start.Add(2*time.Second))
		}
		if sync == nil || sync.offset != 2*time.Second || sync.source != "10.10.0.1" || !sync.stepped {
			t.Errorf("the measurement is %+v, want offset 2s, source 10.10.0.1, stepped", sync)
		}
	})
}

// A failed step does not stop the boot. stepClockAtBoot still returns
// the measurement for status, and the discipline loop slews toward it.
func TestStepClockAtBootKeepsTheMeasurementWhenTheStepFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(-5*time.Second))
		f.stepErr = unix.EPERM

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		if sync == nil || sync.offset != -5*time.Second {
			t.Errorf("the measurement is %+v, want offset -5s", sync)
		}
	})
}

// A boot queries its sources 3 times, over 6 seconds, and then boots on
// the hardware clock, so a network that never answers cannot block it.
func TestStepClockAtBootGivesUpAfterThreeAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t)
		start := time.Now()

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		queries, steps, _, _ := f.recorded()
		if sync != nil || len(steps) != 0 {
			t.Errorf("the measurement is %+v and the steps are %v, want nil and none", sync, steps)
		}
		if queries != 3 {
			t.Errorf("the boot made %d queries, want 3", queries)
		}
		if waited := time.Since(start); waited != 6*time.Second {
			t.Errorf("the boot waited %v, want 6s", waited)
		}
	})
}

// A source that answers on the second attempt is still used. An offset
// under stepThreshold is left for the discipline loop to slew.
func TestStepClockAtBootUsesALateAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, noTimeAnswer, timeAnswer(10*time.Millisecond))
		start := time.Now()

		sync := stepClockAtBoot([]string{"10.10.0.1"})

		_, steps, _, _ := f.recorded()
		if sync == nil || sync.offset != 10*time.Millisecond {
			t.Errorf("the measurement is %+v, want offset 10ms", sync)
		}
		if len(steps) != 0 {
			t.Errorf("the clock steps are %v, want none", steps)
		}
		if waited := time.Since(start); waited != time.Second {
			t.Errorf("the boot waited %v, want 1s", waited)
		}
	})
}
