package main

// follow.go holds the fetch loop that keeps a published tree on the ref
// it follows.

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// follower is one fetch loop for one repository, shared by every volume
// of that URL on this node. The fetch is the repository's work, not the
// volume's, so ten pods on one repository cost one fetch.
type follower struct {
	node       *node
	repository *repository
	cancel     context.CancelFunc
	wake       chan struct{}
	// demanded is the second wake. It runs a pass at once instead of
	// reading the interval again. demand.go sends on it.
	demanded chan struct{}

	mu      sync.Mutex
	volumes map[string]*volume
	// wanted is the volumes a demand named that no pass has answered
	// yet. lastPull is when the last pass ran, which is what a demand
	// waits on.
	wanted   map[string]*volume
	lastPull time.Time
	// backoff is the wait before the loop fetches again for a demand
	// whose fetch failed. Only run reads and writes it.
	backoff time.Duration
}

// maxDemandRetry bounds the wait between fetches for a demand whose
// fetch keeps failing, so a remote that comes back is pulled within
// five minutes.
const maxDemandRetry = 5 * time.Minute

// follow adds a volume to its repository's loop, starting the loop on
// the first volume. The caller holds the node's lock. A volume with
// pull never joins no loop, so a repository every volume pins fetches
// nothing.
func (n *node) follow(mounting *volume) {
	if !mounting.attributes.pull.follows() {
		return
	}
	repo := n.store.repository(mounting.attributes.url)
	loop, found := n.followers[repo.name]
	if !found {
		ctx, cancel := context.WithCancel(n.base)
		loop = &follower{
			node:       n,
			repository: repo,
			cancel:     cancel,
			wake:       make(chan struct{}, 1),
			demanded:   make(chan struct{}, 1),
			volumes:    map[string]*volume{},
			wanted:     map[string]*volume{},
		}
		n.followers[repo.name] = loop
		go loop.run(ctx)
	}
	loop.add(mounting)
}

// unfollow removes a volume from its loop and stops the loop when the
// last volume of the repository goes. The caller holds the node's lock.
func (n *node) unfollow(published *volume) {
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

// remove takes the volume off the loop, and off the demands that wait
// for a pass. A pass after this never counts, notes, or wants the
// volume again, so a series the unstage deletes stays deleted.
func (f *follower) remove(published *volume) int {
	f.mu.Lock()
	delete(f.volumes, published.id)
	delete(f.wanted, published.id)
	left := len(f.volumes)
	f.mu.Unlock()
	f.nudge()
	return left
}

// nudge wakes the loop so it reads the interval again. The channel has
// one slot and the send never blocks, so a publish never waits on a
// loop that is fetching.
func (f *follower) nudge() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// interval is the shortest pull among the volumes that share the
// repository, and false when every one of them pulls on demand alone.
func (f *follower) interval() (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	shortest := time.Duration(0)
	for _, held := range f.volumes {
		every, timed := held.attributes.pull.timer()
		if timed && (shortest == 0 || every < shortest) {
			shortest = every
		}
	}
	return shortest, shortest != 0
}

// run fetches on the interval until the context ends. The context
// descends from the driver's run, so the pod's stop ends every loop.
//
// The second timer is the one a demand inside --demand-min-interval
// waits on. While it waits, a pull is already scheduled, so every
// further demand until it runs is dropped.
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
	f.arm(timer)
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
			f.arm(timer)
		case <-f.demanded:
			if waiting {
				break
			}
			if wait := f.demandWait(time.Now()); wait > 0 {
				delay.Reset(wait)
				waiting = true
				break
			}
			f.settle(f.tick(ctx), retry)
			f.arm(timer)
		case <-delay.C:
			waiting = false
			f.settle(f.tick(ctx), retry)
			f.arm(timer)
		case <-timer.C:
			f.settle(f.tick(ctx), retry)
			f.arm(timer)
		case <-retry.C:
			f.settle(f.tick(ctx), retry)
			f.arm(timer)
		}
	}
}

// arm sets the timer to the interval, and leaves it stopped when no
// volume of the repository names one.
func (f *follower) arm(timer *time.Timer) {
	timer.Stop()
	if every, timed := f.interval(); timed {
		timer.Reset(every)
	}
}

// tick is one pass over the volumes of this repository, under the
// repository's lock, so a fetch never races a publish. It reports
// whether the fetch failed for a volume a demand named.
//
// The pass is timed from its start, so the demands that arrive while it
// fetches are answered by the next pass. A fetch that worked answers
// every demand stamped before that start. A fetch that failed answers
// none, so the volume stays wanted and the loop fetches again after the
// backoff. Without that, a volume with pull on-demand keeps its old
// commit until the next demand.
func (f *follower) tick(ctx context.Context) bool {
	defer f.repository.lock()()
	now := time.Now()
	wanted := f.answered(now)
	failed := false
	for _, held := range f.snapshot() {
		held.reportPulled(now)
		if f.refresh(ctx, held) {
			held.answerDemandsBefore(now)
			continue
		}
		if wanted[held.id] != nil && f.wantAgain(held) {
			failed = true
		}
	}
	return failed
}

// wantAgain puts a volume whose demanded fetch failed back on the
// demands that wait for a pass, and reports whether it did. A volume
// the node removed during the fetch is not put back.
func (f *follower) wantAgain(held *volume) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.volumes[held.id] != held {
		return false
	}
	f.wanted[held.id] = held
	return true
}

// noteHealth records the volume's health, only while the volume is on
// the loop. The check and the note hold the loop's lock, so an unstage
// that removes the volume either comes after the note and deletes the
// series, or comes before and the note does not write it.
func (f *follower) noteHealth(ctx context.Context, held *volume) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.volumes[held.id] == held {
		f.node.noteHealth(ctx, held)
	}
}

// settle sets the retry after a pass. A pass whose demanded fetch
// failed waits --demand-min-interval, then twice as long after each
// further failure up to maxDemandRetry. A pass that worked stops the
// retry.
func (f *follower) settle(failed bool, retry *time.Timer) {
	retry.Stop()
	if !failed {
		f.backoff = 0
		return
	}
	f.backoff = min(max(2*f.backoff, f.node.demandMin), maxDemandRetry)
	retry.Reset(jittered(f.backoff))
}

// jittered is the backoff plus a random part of up to half of it.
// Every node that fetches a repository fails when its remote does, and
// the same doubling would retry them all at the same moment when it
// comes back.
func jittered(backoff time.Duration) time.Duration {
	if backoff < 2 {
		return backoff
	}
	return backoff + rand.N(backoff/2)
}

func (f *follower) snapshot() []*volume {
	f.mu.Lock()
	defer f.mu.Unlock()
	held := make([]*volume, 0, len(f.volumes))
	for _, one := range f.volumes {
		held = append(held, one)
	}
	return held
}

// refresh fetches the volume's ref and, when it moved, places the new
// commit in the published tree. It reports whether the tree now holds
// what the remote holds.
//
// Every path out of a fetch changes what the volume reports, so
// the gauge and the log take the answer here, once, rather than at each
// of them.
func (f *follower) refresh(ctx context.Context, held *volume) bool {
	defer f.noteHealth(ctx, held)
	env, remove, err := held.credentials.use(held.directory)
	if err != nil {
		f.trouble(ctx, held, err.Error())
		return false
	}
	fetchErr := f.node.readings.timeFetch(f.repository.name, func() error {
		return f.repository.fetch(ctx, env, held.attributes.ref, 0)
	})
	remove()
	if fetchErr != nil {
		f.trouble(ctx, held, fetchErr.Error())
		return false
	}
	commit, err := f.repository.resolve(ctx, held.attributes.ref)
	if err != nil {
		f.trouble(ctx, held, err.Error())
		return false
	}
	if standing, _ := held.condition(); standing == commit {
		held.reportCommit(commit)
		return true
	}

	if err := f.repository.place(ctx, commit, held.directory, held.tree); err != nil {
		f.trouble(ctx, held, err.Error())
		return false
	}
	held.reportCommit(commit)
	f.node.logger.InfoContext(ctx, "the tree moved",
		"volume", held.id, "ref", held.attributes.ref, "commit", short(commit))
	return true
}

// trouble records a failed fetch. The first failure after a
// success posts one Event, and the volume's report carries the failure
// until a fetch works again.
func (f *follower) trouble(ctx context.Context, held *volume, message string) {
	if held.reportTrouble(message) {
		f.node.tell(ctx, held, corev1.EventTypeWarning, reasonFailed, message)
	}
	f.node.logger.WarnContext(ctx, "the fetch failed",
		"volume", held.id, "ref", held.attributes.ref, "error", message)
}
