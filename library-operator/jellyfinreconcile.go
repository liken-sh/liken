package main

// jellyfinreconcile.go is the jellyfin role's read of everything Jellyfin
// holds, the second step of the rule in the repository's AGENTS.md. The
// webhook is the role's subscription to Jellyfin, and its posts are not
// retained. So a toggle or a play in Jellyfin while the jellyfin role or
// the progress role is down never arrives as a post. The reconcile reads
// every user's played and resumable items once and publishes each as an
// outside play, dated at its last play in Jellyfin. The progress role
// records each one only where it is newer than the row it holds, so an
// item that did not change writes nothing.
//
// The reconcile runs each time the progress role reports online while the
// webhook listener is up. The progress role's availability is retained,
// so the role's first subscription delivers it, and a start of either
// role runs one reconcile. An outside play is not retained, and a
// reconcile while the progress role is down would publish to nobody.
//
// The reconcile never writes to Jellyfin. It publishes only outside
// plays, and the role reads none of them back.

import (
	"context"
)

// askReconcile asks for one reconcile. An ask while one is already waiting
// folds into it.
func (j *jellyfin) askReconcile() {
	select {
	case j.reconciles <- struct{}{}:
	default:
	}
}

// reconcileLoop runs one reconcile for each ask until the context ends.
// serve starts it only once the webhook listener is up.
func (j *jellyfin) reconcileLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-j.reconciles:
			j.reconcile(ctx)
		}
	}
}

// reconcile reads every user's items and publishes each one. It reads
// through the backfill's code, which publishes the same messages at the
// same pace, and through the role's own index and echoes, so a position
// the role wrote in the last minutes is skipped as its own write.
func (j *jellyfin) reconcile(ctx context.Context) {
	read := &jellyfinBackfill{
		namespace: j.namespace,
		topicBase: j.topicBase,
		api:       j.api,
		index:     j.index,
		echoes:    j.out.echoes,
		publish:   j.publish,
		pace:      jellyfinBackfillPace,
		log:       j.log,
	}
	counts, err := read.run(ctx)
	if err != nil {
		j.logf("the jellyfin reconcile read %d users and could not read %d: %v", counts.users, counts.failed, err)
	}
	j.logf("reconciled %d users with jellyfin: %d plays published, %d items skipped",
		counts.users, counts.published, counts.skipped)
}
