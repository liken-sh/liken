package main

import (
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestAVolumeUnstagedWhileItsDemandWaitsLeavesNoSeries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		n.demandMin = 300 * time.Millisecond
		url := fileURL(repositoryWithACommit(t, map[string]string{"a.txt": "one"}))
		// A second volume with the same URL keeps the loop running after
		// the unstage.
		keeper := demandedVolume(t, n, "keeper", url, "on-demand")
		claimedVolume(t, n, "franchises")
		staged, _ := stagedReadOnly(t, n, "franchises", url, map[string]string{"pull": "on-demand"})

		// The first demand pulls immediately. The later demands wait for
		// --demand-min-interval, and the unstage happens while they wait.
		n.demands.read(t.Context(), annotated(csiVolume("keeper", driverName), demandAt(0)))
		waitForCondition(t, keeper, ", pulled ")
		n.demands.read(t.Context(), annotated(csiVolume("franchises", driverName), demandAt(time.Second)))
		n.demands.read(t.Context(), annotated(csiVolume("keeper", driverName), demandAt(time.Second)))
		if _, err := n.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
			VolumeId: "franchises", StagingTargetPath: staged.StagingTargetPath,
		}); err != nil {
			t.Fatalf("unstaging the volume: %v", err)
		}
		// Two intervals pass, so every waiting demand has had its pass.
		time.Sleep(2 * n.demandMin)
		synctest.Wait()

		if count, found := demandedOf(t, n.metrics, "home", "franchises"); found {
			t.Errorf("git_csi_demanded_pulls_total reads %v for the unstaged volume, want no series", count)
		}
		if abnormal, found := abnormalOf(t, n.metrics, "home", "franchises"); found {
			t.Errorf("git_csi_volume_abnormal reads %v for the unstaged volume, want no series", abnormal)
		}
	})
}

func TestARetryIsNotCountedAsADemandedPull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		n.demandMin = 20 * time.Millisecond
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		demandedVolume(t, n, "franchises", fileURL(source), "on-demand")
		if err := os.Rename(source, source+".away"); err != nil {
			t.Fatalf("moving the remote away: %v", err)
		}

		n.demands.read(t.Context(), annotated(csiVolume("franchises", driverName), demandAt(0)))
		// The fetch fails, and the loop retries several times during
		// this sleep.
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()

		if count, _ := demandedOf(t, n.metrics, "home", "franchises"); count != 1 {
			t.Errorf("one demand counted %v demanded pulls, want 1", count)
		}
	})
}

func TestTheRetryWaitIsSpreadAboveTheFloor(t *testing.T) {
	for _, c := range []struct {
		backoff, floor, lowest time.Duration
	}{
		{backoff: 10 * time.Second, floor: 0, lowest: 5 * time.Second},
		{backoff: 40 * time.Second, floor: 10 * time.Second, lowest: 20 * time.Second},
		{backoff: 40 * time.Second, floor: 30 * time.Second, lowest: 30 * time.Second},
	} {
		t.Run(c.backoff.String()+" over "+c.floor.String(), func(t *testing.T) {
			waits := map[time.Duration]bool{}
			for range 50 {
				wait := jittered(c.backoff, c.floor)
				if wait < c.lowest || wait > c.backoff {
					t.Fatalf("the wait is %s, want %s to %s", wait, c.lowest, c.backoff)
				}
				waits[wait] = true
			}
			// The waits have to vary, so nodes that failed together do
			// not all retry at the same moment.
			if len(waits) < 2 {
				t.Errorf("50 waits had %d distinct values, want them spread", len(waits))
			}
		})
	}
}

func TestEveryRetryWaitsAtLeastTheDemandIntervalAndAtMostFiveMinutes(t *testing.T) {
	loop := detachedLoop()
	loop.node.demandMin = 10 * time.Second
	for failure := range 20 {
		wait := loop.nextRetry()
		if wait < loop.node.demandMin || wait > maxDemandRetry {
			t.Fatalf("retry %d waits %s, want %s to %s",
				failure+1, wait, loop.node.demandMin, maxDemandRetry)
		}
	}
}

// detachedLoop returns a follower with no volumes, which is the state
// after an unstage removed them.
func detachedLoop() *follower {
	return &follower{
		node:     &node{},
		demanded: make(chan struct{}, 1),
		volumes:  map[string]*volume{},
		wanted:   map[string]*volume{},
	}
}

func TestAVolumeOffTheLoopIsNeitherDemandedNorWantedAgain(t *testing.T) {
	for _, c := range []struct {
		name string
		act  func(loop *follower, vol *volume)
	}{
		{name: "a demand after the unstage", act: func(loop *follower, vol *volume) { loop.demand(vol) }},
		{name: "a failed fetch that ended after the unstage", act: func(loop *follower, vol *volume) { loop.wantAgain(vol) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			loop := detachedLoop()
			c.act(loop, &volume{id: "franchises"})

			if len(loop.wanted) != 0 || len(loop.demanded) != 0 {
				t.Errorf("the loop follows %d volumes and has %d wakes, want none",
					len(loop.wanted), len(loop.demanded))
			}
		})
	}
}

func TestNoBackoffHasNoJitter(t *testing.T) {
	for _, backoff := range []time.Duration{0, 1} {
		if got := jittered(backoff, 0); got != backoff {
			t.Errorf("jittered(%s) is %s, want %s", backoff, got, backoff)
		}
	}
}
