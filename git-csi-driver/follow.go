package main

// follow.go runs the loop that fetches a repository and moves each
// volume's published tree to the commit its ref now points at.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// follower is the fetch loop for one repository on this node. Every
// volume with the repository's URL shares the loop, because a fetch
// updates the node's copy of the repository and not one volume.
type follower struct {
	node       *node
	repository *repository
	cancel     context.CancelFunc
	// wake tells the loop to recompute its timer, because a volume
	// joined or left.
	wake chan struct{}
	// demanded tells the loop to run a pass now. demand.go sends on it.
	demanded chan struct{}
	// republishWake tells the loop to run a pass now, because a republish
	// brought back a credential that a restart lost, or brought a
	// rotated one. republish.go sends on it.
	republishWake chan struct{}

	mu      sync.Mutex
	volumes map[string]*volume
	// wanted holds the volumes that a demand named and no pass has run
	// for yet. lastPull is the start time of the last pass, which a
	// demand compares against --demand-min-interval.
	wanted   map[string]*volume
	lastPull time.Time
	// backoff is the current wait before the loop retries a fetch that
	// failed for a demand. Only run's goroutine reads and writes it.
	backoff time.Duration
}

// maxDemandRetry is the longest wait between retries of a fetch that
// keeps failing for a demand, so a remote that comes back is fetched
// within this time.
const maxDemandRetry = 5 * time.Minute

// joinFetchLoop adds the volume to its repository's loop, and starts the
// loop when the volume is the first. A volume whose pull is "never" joins no
// loop, so a repository that only such volumes use is never fetched
// after the stage. The caller holds the node's lock.
func (n *node) joinFetchLoop(mounting *volume) {
	if !mounting.attributes.pull.follows() {
		return
	}
	repo := n.store.repository(mounting.attributes.url)
	loop, found := n.followers[repo.name]
	if !found {
		ctx, cancel := context.WithCancel(n.base)
		loop = &follower{
			node:          n,
			repository:    repo,
			cancel:        cancel,
			wake:          make(chan struct{}, 1),
			demanded:      make(chan struct{}, 1),
			republishWake: make(chan struct{}, 1),
			volumes:       map[string]*volume{},
			wanted:        map[string]*volume{},
		}
		n.followers[repo.name] = loop
		go loop.run(ctx)
	}
	loop.add(mounting)
}

// leaveFetchLoop stops the loop when the volume was its last. The caller
// holds the node's lock.
func (n *node) leaveFetchLoop(published *volume) {
	if !published.attributes.pull.follows() {
		return
	}
	repo := n.store.repository(published.attributes.url)
	loop, found := n.followers[repo.name]
	if !found {
		return
	}
	if loop.remove(published) == 0 {
		loop.cancel()
		delete(n.followers, repo.name)
	}
}

func (f *follower) add(mounting *volume) {
	f.mu.Lock()
	f.volumes[mounting.id] = mounting
	f.mu.Unlock()
	f.nudge()
}

// remove takes the volume off the loop and off the demands that wait
// for a pass, and returns how many volumes remain. A pass that starts
// after remove does not touch the volume's metrics, so the series that
// the unstage deletes is not created again.
func (f *follower) remove(published *volume) int {
	f.mu.Lock()
	delete(f.volumes, published.id)
	delete(f.wanted, published.id)
	left := len(f.volumes)
	f.mu.Unlock()
	f.nudge()
	return left
}

// nudge tells the loop to recompute its timer. The send never blocks,
// so a publish does not wait for a loop that is in the middle of a
// fetch.
func (f *follower) nudge() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *follower) interval() (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	shortest := time.Duration(0)
	for _, vol := range f.volumes {
		every, timed := vol.attributes.pull.timer()
		if timed && (shortest == 0 || every < shortest) {
			shortest = every
		}
	}
	return shortest, shortest != 0
}

// run runs a pass when the interval timer fires, on a demand, on a
// republish that brings a credential, and on a retry, until ctx ends. A
// wake only restarts the interval timer.
//
// A demand that arrives sooner than --demand-min-interval after the
// last pass waits on the delay timer. While it waits, run drops further
// demands, because the pass at the end of the delay fetches for them too.
// The retry timer runs a pass again after a fetch failed for a demand.
func (f *follower) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	delay := time.NewTimer(time.Hour)
	defer delay.Stop()
	delay.Stop()
	retry := time.NewTimer(time.Hour)
	defer retry.Stop()
	retry.Stop()
	waiting := false
	f.resetTimer(timer)
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
			f.resetTimer(timer)
		case <-f.demanded:
			if waiting {
				break
			}
			if wait := f.demandWait(time.Now()); wait > 0 {
				delay.Reset(wait)
				waiting = true
				break
			}
			f.setRetry(f.runPass(ctx), retry)
			f.resetTimer(timer)
		case <-delay.C:
			waiting = false
			f.setRetry(f.runPass(ctx), retry)
			f.resetTimer(timer)
		case <-f.republishWake:
			f.setRetry(f.runPass(ctx), retry)
			f.resetTimer(timer)
		case <-timer.C:
			f.setRetry(f.runPass(ctx), retry)
			f.resetTimer(timer)
		case <-retry.C:
			f.setRetry(f.runPass(ctx), retry)
			f.resetTimer(timer)
		}
	}
}

func (f *follower) resetTimer(timer *time.Timer) {
	timer.Stop()
	if every, timed := f.interval(); timed {
		timer.Reset(every)
	}
}

// runPass runs one pass over the repository's volumes while it holds the
// repository's lock, so a fetch does not race a publish. It returns
// true when a fetch failed for a volume that a demand named.
//
// runPass skips a volume that waits for its credential. Each volume
// fetches with its own credential, so a fetch without it can only fail.
// The republish that brings the credential starts its own pass.
//
// A fetch that succeeds satisfies every demand made before the pass
// started. A demand that arrives during the pass is left for the next
// pass. A fetch that fails satisfies no demand, so runPass puts the volume
// back in wanted and the loop retries after the backoff. Without the
// retry, a volume with pull on-demand stays on its old commit until the
// next demand.
func (f *follower) runPass(ctx context.Context) bool {
	defer f.repository.lock()()
	now := time.Now()
	wanted := f.startPass(now)
	failed := false
	for _, vol := range f.snapshot() {
		if vol.waitsForCredential() {
			continue
		}
		vol.reportPulled(now)
		if f.refresh(ctx, vol) {
			vol.satisfyDemandsBefore(now)
			continue
		}
		if wanted[vol.id] != nil && f.wantAgain(vol) {
			failed = true
		}
	}
	return failed
}

// wantAgain puts a volume whose fetch failed for a demand back in
// wanted, and returns whether it did. A volume that the node removed
// during the fetch is not put back.
func (f *follower) wantAgain(vol *volume) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.volumes[vol.id] != vol {
		return false
	}
	f.wanted[vol.id] = vol
	return true
}

// recordHealth records the volume's health only while the volume is on
// the loop. It checks and records under the loop's lock. An unstage
// that removes the volume then either runs after the record and deletes
// the series, or runs before and the record is never written.
func (f *follower) recordHealth(ctx context.Context, vol *volume) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.volumes[vol.id] == vol {
		f.node.recordHealth(ctx, vol)
	}
}

func (f *follower) setRetry(failed bool, retry *time.Timer) {
	retry.Stop()
	if !failed {
		f.backoff = 0
		return
	}
	retry.Reset(f.nextRetry())
}

// nextRetry doubles the backoff, from --demand-min-interval up to
// maxDemandRetry, and returns the wait before the next fetch. A retry
// does not go through the delay timer that limits demands, so the wait
// itself has to stay at or above --demand-min-interval.
func (f *follower) nextRetry() time.Duration {
	f.backoff = min(max(2*f.backoff, f.node.demandMin), maxDemandRetry)
	return jittered(f.backoff, f.node.demandMin)
}

// jittered returns a random wait between half the backoff and the
// backoff, raised to floor when half the backoff is below it. Every
// node that fetches a repository fails when its remote does, and equal
// backoffs would make them all retry at the same moment when the remote
// returns. The wait never exceeds backoff, so it never exceeds
// maxDemandRetry.
func jittered(backoff, floor time.Duration) time.Duration {
	if backoff < 2 {
		return backoff
	}
	lowest := max(floor, backoff/2)
	if lowest >= backoff {
		return backoff
	}
	return lowest + rand.N(backoff-lowest+1)
}

func (f *follower) snapshot() []*volume {
	f.mu.Lock()
	defer f.mu.Unlock()
	vol := make([]*volume, 0, len(f.volumes))
	for _, one := range f.volumes {
		vol = append(vol, one)
	}
	return vol
}

// refresh fetches the volume's ref and, when the commit changed,
// places it in the published tree. It returns true when the tree is on
// the commit that the ref now points at.
//
// Every return changes the volume's health, so the deferred recordHealth
// records it once for all of them.
func (f *follower) refresh(ctx context.Context, vol *volume) bool {
	defer f.recordHealth(ctx, vol)
	env, remove, err := vol.credential().use(vol.directory)
	if err != nil {
		f.recordFailure(ctx, vol, err.Error())
		return false
	}
	fetchErr := f.node.metrics.timeFetch(f.repository.name, func() error {
		return f.repository.fetch(ctx, env, vol.attributes.ref, 0)
	})
	remove()
	if fetchErr != nil {
		f.recordFailure(ctx, vol, fetchErr.Error())
		return false
	}
	commit, err := f.repository.resolve(ctx, vol.attributes.ref)
	if err != nil {
		f.recordFailure(ctx, vol, err.Error())
		return false
	}
	if current, _ := vol.commitAndFault(); current == commit {
		f.recordRecovery(vol, commit)
		return true
	}

	if err := f.repository.place(ctx, commit, vol.directory, vol.tree); err != nil {
		f.recordFailure(ctx, vol, err.Error())
		return false
	}
	f.recordRecovery(vol, commit)
	f.node.logger.InfoContext(ctx, "The tree of the volume moved",
		"volume", vol.id, "ref", vol.attributes.ref, "commit", short(commit))
	return true
}

// recordRecovery records the commit of a successful fetch. The first
// success after a failure posts a Normal Event, so `kubectl describe`
// shows when the Warning from the failure ended.
func (f *follower) recordRecovery(vol *volume, commit string) {
	if vol.reportCommit(commit) {
		f.node.postVolumeEvent(vol, corev1.EventTypeNormal, reasonRecovered,
			fmt.Sprintf("Fetched %s at %s after the previous fetch failed", vol.attributes.ref, short(commit)))
	}
}

// recordFailure records a failed fetch. The first failure after a
// success posts a Warning Event. The volume keeps the message in its
// health report until a fetch succeeds.
func (f *follower) recordFailure(ctx context.Context, vol *volume, message string) {
	if vol.reportFetchFailed(message) {
		f.node.postVolumeEvent(vol, corev1.EventTypeWarning, reasonFailed,
			fmt.Sprintf("The fetch of %s failed: %s", vol.attributes.ref, message))
	}
	f.node.logger.WarnContext(ctx, "The repository fetch for the volume did not succeed",
		"volume", vol.id, "ref", vol.attributes.ref, "error", message)
}
