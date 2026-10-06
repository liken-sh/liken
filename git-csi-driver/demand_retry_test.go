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
		answering, _ := testNode(t, io.Discard)
		answering.demandMin = 300 * time.Millisecond
		url := fileURL(repositoryWithACommit(t, map[string]string{"a.txt": "one"}))
		// A second volume of the same URL keeps the loop running after the
		// unstage.
		keeper := demandedVolume(t, answering, "keeper", url, "on-demand")
		claimedVolume(t, answering, "franchises")
		staged, _ := stagedReadOnly(t, answering, "franchises", url, map[string]string{"pull": "on-demand"})

		// The first demand pulls at once. The next ones wait out the
		// interval, and the unstage comes while they wait.
		answering.demands.read(t.Context(), annotated(csiVolume("keeper", driverName), demandAt(0)))
		waitForCondition(t, keeper, ", pulled ")
		answering.demands.read(t.Context(), annotated(csiVolume("franchises", driverName), demandAt(time.Second)))
		answering.demands.read(t.Context(), annotated(csiVolume("keeper", driverName), demandAt(time.Second)))
		if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
			VolumeId: "franchises", StagingTargetPath: staged.StagingTargetPath,
		}); err != nil {
			t.Fatalf("NodeUnstageVolume: %v", err)
		}
		// Two intervals pass, so every demand that waited has had its
		// pass.
		time.Sleep(2 * answering.demandMin)
		synctest.Wait()

		if counted, found := demandedOf(t, answering.readings, "home", "franchises"); found {
			t.Errorf("git_csi_demanded_pulls_total reads %v for the unstaged volume, want no series", counted)
		}
		if abnormal, found := abnormalOf(t, answering.readings, "home", "franchises"); found {
			t.Errorf("git_csi_volume_abnormal reads %v for the unstaged volume, want no series", abnormal)
		}
	})
}

func TestARetryIsNotCountedAsADemandedPull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		answering.demandMin = 20 * time.Millisecond
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		demandedVolume(t, answering, "franchises", fileURL(source), "on-demand")
		if err := os.Rename(source, source+".away"); err != nil {
			t.Fatalf("moving the remote away: %v", err)
		}

		answering.demands.read(t.Context(), annotated(csiVolume("franchises", driverName), demandAt(0)))
		// The fetch fails, and the loop fetches again several times inside
		// the pause.
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()

		if counted, _ := demandedOf(t, answering.readings, "home", "franchises"); counted != 1 {
			t.Errorf("one demand counted %v demanded pulls, want 1", counted)
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
			// Nodes that fail together retry at different times, so a
			// remote that comes back is not fetched by every node at
			// once.
			if len(waits) < 2 {
				t.Errorf("50 waits took %d values, want them spread", len(waits))
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

// detachedLoop is a loop that holds none of the volumes a test hands
// it, the state after an unstage took them off.
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
		act  func(loop *follower, held *volume)
	}{
		{name: "a demand after the unstage", act: func(loop *follower, held *volume) { loop.demand(held) }},
		{name: "a failed fetch that ended after the unstage", act: func(loop *follower, held *volume) { loop.wantAgain(held) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			loop := detachedLoop()
			c.act(loop, &volume{id: "franchises"})

			if len(loop.wanted) != 0 || len(loop.demanded) != 0 {
				t.Errorf("the loop wants %d volumes and holds %d wakes, want none",
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
