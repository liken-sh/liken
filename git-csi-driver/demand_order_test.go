package main

import (
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

// These tests read one old or one new demand in every order the list,
// the watch, the stage, and a restart can take on a node. The node acts
// on a demand by its time alone, so no order pulls for an old demand
// and none misses a new one.

func TestAStageBeforeTheFirstListIgnoresAnOldDemand(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
	demandPull(t, answering, "franchises", oldDemand)

	watchDemands(t, answering)
	time.Sleep(300 * time.Millisecond)

	noDemand(t, held)
	if counted, found := demandedOf(t, answering.readings, "home", "franchises"); found {
		t.Errorf("an old annotation counted %v demanded pulls, want none", counted)
	}
}

func TestARestartCountsOneDemandForAnOldAnnotation(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.demandMin = 50 * time.Millisecond
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
	demandPull(t, answering, "franchises", oldDemand)

	again, _ := testNode(t, io.Discard)
	again.demandMin = 50 * time.Millisecond
	again.store = answering.store
	again.events = answering.events
	again.arms.client = answering.arms.client
	again.mounted = func(string) bool { return true }
	again.demands = newDemanding(again, cluster(t, answering), slog.New(slog.NewTextHandler(io.Discard, nil)))
	again.demands.retry = 20 * time.Millisecond
	again.resume(t.Context())
	again.mu.Lock()
	resumed := again.staged["franchises"]
	again.mu.Unlock()
	// The restart's own pull runs first, as it does when the list is
	// slower than the pass.
	waitForCondition(t, resumed, ", pulled ")
	go again.demands.follow(t.Context())
	time.Sleep(500 * time.Millisecond)

	if counted, _ := demandedOf(t, again.readings, "home", resumed.id); counted > 1 {
		t.Errorf("a restart with an old annotation counted %v demanded pulls, want at most 1", counted)
	}
}

func TestAStageDuringARelistIgnoresAnOldDemand(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	old := annotated(csiVolume("franchises", driverName), oldDemand)
	answering.demands.read(t.Context(), old)
	// A relist reads the old value again while the stage looks up its
	// claim.
	var once sync.Once
	cluster(t, answering).PrependReactor("list", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			once.Do(func() { answering.demands.read(t.Context(), old) })
			return false, nil, nil
		})
	held := demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")

	noDemand(t, held)
}

func TestARestartedWriteableVolumeIgnoresAnOldDemand(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	boundVolume(t, answering, "config", "")
	stagedWriteable(t, answering, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))

	logs := &logbook{}
	again, _ := testNode(t, logs)
	again.store = answering.store
	again.events = answering.events
	again.arms.client = answering.arms.client
	again.mounted = func(string) bool { return true }
	again.resume(t.Context())
	again.demands.read(t.Context(), writeableDemand(oldDemand))

	if strings.Contains(logs.String(), "the demand did nothing") {
		t.Errorf("an old demand was acted on after the restart (%q)", logs)
	}
}

// demandsActedOn stages the writeable volume config, reads a demand at
// each time, and counts the demands the node acted on.
func demandsActedOn(t *testing.T, stamps ...string) int {
	t.Helper()
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	boundVolume(t, answering, "config", "")
	stagedWriteable(t, answering, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))
	for _, at := range stamps {
		answering.demands.read(t.Context(), writeableDemand(at))
	}
	return strings.Count(logs.String(), "the demand did nothing")
}

func TestADemandIsActedOnByItsTime(t *testing.T) {
	for _, c := range []struct {
		name   string
		stamps []string
		want   int
	}{
		{
			name:   "a demand stamped after the stage",
			stamps: []string{demandAt(0)},
			want:   1,
		},
		{
			name:   "a demand stamped inside the skew before the stage",
			stamps: []string{demandAt(-demandSkew / 2)},
			want:   1,
		},
		{
			name:   "a demand stamped beyond the skew before the stage",
			stamps: []string{demandAt(-2 * demandSkew)},
			want:   0,
		},
		{
			name:   "the same demand read twice",
			stamps: []string{demandAt(0), demandAt(0)},
			want:   1,
		},
		{
			name:   "an older demand after a newer one",
			stamps: []string{demandAt(2 * time.Second), demandAt(0)},
			want:   1,
		},
		{
			name:   "a later demand after one stamped in the future",
			stamps: []string{demandAt(time.Hour), demandAt(2 * time.Second)},
			want:   2,
		},
		{
			name:   "a value that is not a time",
			stamps: []string{"soon"},
			want:   0,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := demandsActedOn(t, c.stamps...); got != c.want {
				t.Errorf("the node acted on %d demands, want %d", got, c.want)
			}
		})
	}
}

func TestADemandThatIsNotATimeSaysSo(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)

	answering.demands.read(t.Context(), annotated(csiVolume("franchises", driverName), "soon"))

	if !strings.Contains(logs.String(), "the demand is not a time") {
		t.Errorf("the log is %q, want the value that is not a time in it", logs)
	}
}
