package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

// These tests deliver one old demand or one new demand in every order
// that the list, the watch, the stage, and a restart can occur on a
// node. The node acts on a demand by its timestamp alone, so no order
// may pull for an old demand or miss a new one.

func TestAStageBeforeTheFirstListIgnoresAnOldDemand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
		demandPull(t, n, "franchises", oldDemand)

		watchDemands(t, n)
		synctest.Wait()

		noDemand(t, vol)
		if count, found := demandedOf(t, n.metrics, "home", "franchises"); found {
			t.Errorf("an old annotation counted %v demanded pulls, want none", count)
		}
	})
}

func TestARestartCountsOneDemandForAnOldAnnotation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		n.demandMin = 50 * time.Millisecond
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
		demandPull(t, n, "franchises", oldDemand)

		again, _ := testNode(t, io.Discard)
		again.demandMin = 50 * time.Millisecond
		again.store = n.store
		again.events = n.events
		again.arming.client = n.arming.client
		again.mounted = func(string) bool { return true }
		again.demands = newDemandWatch(again, cluster(t, n), slog.New(slog.NewTextHandler(io.Discard, nil)))
		again.resume(t.Context())
		again.mu.Lock()
		resumed := again.staged["franchises"]
		again.mu.Unlock()
		// The restart pulls first. Wait for that pull before the demand loop
		// starts.
		waitForCondition(t, resumed, ", pulled ")
		go again.demands.run(t.Context())
		// Advance the clock ten intervals, enough for a second demanded pull
		// if one were wrongly started.
		time.Sleep(10 * again.demandMin)
		synctest.Wait()

		if count, _ := demandedOf(t, again.metrics, "home", resumed.id); count > 1 {
			t.Errorf("a restart with an old annotation counted %v demanded pulls, want at most 1", count)
		}
	})
}

func TestAStageDuringARelistIgnoresAnOldDemand(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	old := annotated(csiVolume("franchises", driverName), oldDemand)
	n.demands.read(t.Context(), old)
	// A relist delivers the old annotation again while the stage looks up
	// its claim.
	var once sync.Once
	cluster(t, n).PrependReactor("list", "persistentvolumes",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			once.Do(func() { n.demands.read(t.Context(), old) })
			return false, nil, nil
		})
	vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")

	noDemand(t, vol)
}

func TestARestartedWriteableVolumeIgnoresAnOldDemand(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	boundVolume(t, n, "config", "")
	stagedWriteable(t, n, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))

	logs := &logbook{}
	again, _ := testNode(t, logs)
	again.store = n.store
	again.events = n.events
	again.arming.client = n.arming.client
	again.mounted = func(string) bool { return true }
	again.resume(t.Context())
	again.demands.read(t.Context(), writeableDemand(oldDemand))

	if strings.Contains(logs.String(), "Ignored a pull demand") {
		t.Errorf("an old demand was acted on after the restart (%q)", logs)
	}
}

// demandsActedOn stages a writeable volume, delivers a demand with each
// of the timestamps, and returns how many demands the node accepted. The
// node starts no pull for a writeable volume. It logs "the demand did
// nothing" for each demand it accepts, so the test counts that line.
func demandsActedOn(t *testing.T, stamps ...string) int {
	t.Helper()
	logs := &logbook{}
	n, _ := testNode(t, logs)
	boundVolume(t, n, "config", "")
	stagedWriteable(t, n, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})))
	for _, at := range stamps {
		n.demands.read(t.Context(), writeableDemand(at))
	}
	return strings.Count(logs.String(), "Ignored a pull demand")
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
			want:   2,
		},
		{
			name:   "a second demand from a writer whose clock runs behind",
			stamps: []string{demandAt(0), demandAt(-demandSkew / 3)},
			want:   2,
		},
		{
			name:   "a demand stamped in the future read twice",
			stamps: []string{demandAt(time.Hour), demandAt(time.Hour)},
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

func TestADemandThatIsNotATimeIsLogged(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)

	n.demands.read(t.Context(), annotated(csiVolume("franchises", driverName), "soon"))

	if !strings.Contains(logs.String(), "The pull annotation is not an RFC 3339 time") {
		t.Errorf("the log is %q, want the value that is not a time in it", logs)
	}
}

func TestADemandWhoseFetchFailsIsFetchedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		n.demandMin = 50 * time.Millisecond
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		vol := demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
		want := commitFiles(t, source, map[string]string{"a.txt": "two"})

		// The remote is gone when the demand arrives, so the pull that the
		// demand starts fails.
		away := source + ".away"
		if err := os.Rename(source, away); err != nil {
			t.Fatalf("moving the remote away: %v", err)
		}
		n.demands.read(t.Context(),
			annotated(csiVolume("franchises", driverName), demandAt(0)))
		synctest.Wait()
		if _, fault := vol.commitAndFault(); fault == "" {
			t.Fatal("a fetch from a remote that is gone left the health report empty, want it to report the fetch")
		}
		if err := os.Rename(away, source); err != nil {
			t.Fatalf("moving the remote back: %v", err)
		}

		// Advance the clock by the longest retry wait, so the loop fetches
		// again.
		time.Sleep(maxDemandRetry)
		waitForCommit(t, vol, want)
	})
}
