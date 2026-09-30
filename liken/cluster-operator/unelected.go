package main

// Acting without an election, while the API server refuses the Lease.
//
// The OS images carry this binary, and a leader's boot applies the
// manifests that hold its RBAC. The image tag resolves on each node to
// the build that node's OS carries, so this binary can run under the
// RBAC of a release from before the election, which grants no write of
// the Lease. The case is the normal path of a rollback: the leader that
// goes first applies the old manifests, and the new pod can land on a
// node that still runs this build. A copy that waited for the Lease
// there would grant nothing, and the rest of the rollback needs its
// grants.
//
// So a copy acts without an election while the API server refuses the
// Lease itself: a 403 Forbidden on any request of the Lease, which says
// the RBAC is missing, or a 404 NotFound on its create, which says the
// Lease resource type is missing. That is how every release before the
// election acted. Its manifests run one replica with the Recreate
// strategy, and a second copy existed only in the cases the election
// now covers. Any other error, such as a timeout, a 5xx, or a
// conflict, is an ordinary failure of the election, and the copy does
// not act.
//
// The elector keeps trying the Lease every retry period. When the Lease
// answers, the copy either takes it and leads, or finds another holder
// and stops acting. Two copies can act at once for up to one retry
// period, 5 to 11 seconds: a copy in this mode learns that another
// copy leads only at its next try. That overlap is the same one the
// releases before the election had whenever two copies ran.

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// classifyRead judges one read of the Lease. A refusal marks the Lease
// refused. A Lease that another copy holds and renewed within its
// duration ends the mode, because that copy leads. A Lease that does
// not exist says nothing yet: the elector creates it next. Any other
// error ends the mode.
func (r *renewalClock) classifyRead(record *resourcelock.LeaderElectionRecord, err error) {
	switch {
	case apierrors.IsForbidden(err):
		r.answered(true)
	case apierrors.IsNotFound(err):
	case err != nil:
		r.answered(false)
	case record.HolderIdentity != "" && record.HolderIdentity != r.Identity() &&
		time.Since(record.RenewTime.Time) < operatorLeaseTiming.duration:
		r.answered(false)
	case record.HolderIdentity == r.Identity() && r.leading():
		r.answered(false)
	}
}

// classifyWrite judges one create or update of the Lease. A refusal, or
// a create whose resource type does not exist, marks the Lease refused.
// A renewal that succeeded while this copy leads ends the mode, so a
// single refused renewal does not leave the write guard off for the
// rest of the lead. A first write that succeeded says nothing here:
// the elector starts the lead after it, and OnStartedLeading ends the
// mode. Any other error ends the mode. An update that answers 404 is
// not a refusal: the Lease was deleted between the read and the write.
func (r *renewalClock) classifyWrite(err error, create bool) {
	switch {
	case err == nil && r.leading():
		r.answered(false)
	case err == nil:
	case apierrors.IsForbidden(err), create && apierrors.IsNotFound(err):
		r.answered(true)
	default:
		r.answered(false)
	}
}

// leaseAnswered sets the mode, and reports each change once: a log line
// and the gauge.
func (l *leadership) leaseAnswered(refused bool) {
	if l.unelected.Swap(refused) == refused {
		return
	}
	subject := "lease " + leaseNamespace + "/" + leaseName
	if refused {
		l.report(fmt.Sprintf("%s: the API server refuses %s; acting without an election until it answers", component, subject))
	} else {
		l.report(fmt.Sprintf("%s: %s answers again; acting without an election ends", component, subject))
	}
	if l.setUnelected != nil {
		l.setUnelected(refused)
	}
	l.signalMode()
}

// leads answers whether the elector has started this copy's lead.
func (l *leadership) leads() bool {
	select {
	case <-l.started:
		return true
	default:
		return false
	}
}

func (l *leadership) signalMode() {
	select {
	case l.modeChanged <- struct{}{}:
	default:
	}
}

// acting answers whether this copy may sweep now: it leads, or it acts
// without an election.
func (l *leadership) acting() bool {
	if l.stepping.Load() {
		return false
	}
	if l.unelected.Load() {
		return true
	}
	select {
	case <-l.started:
		return true
	default:
		return false
	}
}

// mayAct blocks until this copy may sweep, and answers true, or answers
// false when stop ends first. A copy that acted without an election and
// then found another holder waits here until it takes the Lease.
func (l *leadership) mayAct(stop context.Context) bool {
	for !l.acting() {
		select {
		case <-l.started:
		case <-l.modeChanged:
		case <-stop.Done():
			return false
		}
	}
	return stop.Err() == nil
}
