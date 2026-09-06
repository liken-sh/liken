package main

// follow.go holds the fetch loop that keeps a published tree on the ref
// it follows.

import (
	"context"
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

	mu      sync.Mutex
	volumes map[string]*volume
}

// follow adds a volume to its repository's loop, starting the loop on
// the first volume. The caller holds the node's lock. A volume with
// pull never joins no loop, so a repository every volume pins fetches
// nothing.
func (n *node) follow(mounting *volume) {
	if mounting.attributes.pull == 0 {
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
			volumes:    map[string]*volume{},
		}
		n.followers[repo.name] = loop
		go loop.run(ctx)
	}
	loop.add(mounting)
}

// unfollow removes a volume from its loop and stops the loop when the
// last volume of the repository goes. The caller holds the node's lock.
func (n *node) unfollow(published *volume) {
	if published.attributes.pull == 0 {
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

func (f *follower) remove(published *volume) int {
	f.mu.Lock()
	delete(f.volumes, published.id)
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
// repository.
func (f *follower) interval() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	shortest := time.Duration(0)
	for _, held := range f.volumes {
		if shortest == 0 || held.attributes.pull < shortest {
			shortest = held.attributes.pull
		}
	}
	if shortest == 0 {
		shortest = defaultPull
	}
	return shortest
}

// run fetches on the interval until the context ends. The context
// descends from the driver's run, so the pod's stop ends every loop.
func (f *follower) run(ctx context.Context) {
	timer := time.NewTimer(f.interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
			timer.Reset(f.interval())
		case <-timer.C:
			f.tick(ctx)
			timer.Reset(f.interval())
		}
	}
}

// tick is one pass over the volumes of this repository, under the
// repository's lock, so a fetch never races a publish.
func (f *follower) tick(ctx context.Context) {
	defer f.repository.lock()()
	for _, held := range f.snapshot() {
		f.refresh(ctx, held)
	}
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
// commit in the published tree.
//
// Every path out of a fetch changes what the volume reports, so
// the gauge and the log take the answer here, once, rather than at each
// of them.
func (f *follower) refresh(ctx context.Context, held *volume) {
	defer f.node.noteHealth(ctx, held)
	env, remove, err := held.credentials.use(held.directory)
	if err != nil {
		f.trouble(ctx, held, err.Error())
		return
	}
	fetchErr := f.repository.fetch(ctx, env, held.attributes.ref, 0)
	remove()
	if fetchErr != nil {
		f.trouble(ctx, held, fetchErr.Error())
		return
	}
	commit, err := f.repository.resolve(ctx, held.attributes.ref)
	if err != nil {
		f.trouble(ctx, held, err.Error())
		return
	}
	if standing, _ := held.condition(); standing == commit {
		held.reportCommit(commit)
		return
	}

	if err := f.repository.place(ctx, commit, held.directory, held.tree); err != nil {
		f.trouble(ctx, held, err.Error())
		return
	}
	held.reportCommit(commit)
	f.node.logger.InfoContext(ctx, "the tree moved",
		"volume", held.id, "ref", held.attributes.ref, "commit", short(commit))
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
