package election

// Acting without an election, while the API server refuses the Lease.
//
// Only liken's cluster operator turns this on (Options.ActWhenRefused).
// The OS images carry its binary, and a leader's boot applies the
// manifests that hold its RBAC. The image tag resolves on each node to
// the build that node's OS carries, so the binary can run under the
// RBAC of a release from before the election, which grants no write of
// the Lease. The case is the normal path of a rollback: the leader that
// goes first applies the old manifests, and the new pod can land on a
// node that still runs the new build. A copy that waited for the Lease
// there would grant no reboot, and the rest of the rollback needs its
// grants. Every other operator's manifest carries its RBAC and its image
// together, so its RBAC never lags its binary.
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
func (l *Lead) classifyRead(record *resourcelock.LeaderElectionRecord, err error) {
	switch {
	case apierrors.IsForbidden(err):
		l.leaseAnswered(true)
	case apierrors.IsNotFound(err):
	case err != nil:
		l.leaseAnswered(false)
	case record.HolderIdentity != "" && record.HolderIdentity != l.identity &&
		time.Since(record.RenewTime.Time) < Duration:
		l.leaseAnswered(false)
	case record.HolderIdentity == l.identity && l.Leads():
		l.leaseAnswered(false)
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
func (l *Lead) classifyWrite(err error, create bool) {
	switch {
	case err == nil && l.Leads():
		l.leaseAnswered(false)
	case err == nil:
	case apierrors.IsForbidden(err), create && apierrors.IsNotFound(err):
		l.leaseAnswered(true)
	default:
		l.leaseAnswered(false)
	}
}

// leaseAnswered sets the mode, and reports each change once: a log line
// and Options.Unelected. A copy without ActWhenRefused never enters the
// mode.
func (l *Lead) leaseAnswered(refused bool) {
	if refused && !l.options.ActWhenRefused {
		return
	}
	if l.unelected.Swap(refused) == refused {
		return
	}
	if refused {
		l.options.Report(fmt.Sprintf("the API server refuses %s; acting without an election until it answers", l.subject()))
	} else {
		l.options.Report(fmt.Sprintf("%s answers again; acting without an election ends", l.subject()))
	}
	if l.options.Unelected != nil {
		l.options.Unelected(refused)
	}
	l.signalMode()
}

func (l *Lead) signalMode() {
	select {
	case l.modeChanged <- struct{}{}:
	default:
	}
}

// acting answers whether this copy may act now: it leads, or it acts
// without an election.
func (l *Lead) acting() bool {
	if l.stepping.Load() {
		return false
	}
	return l.unelected.Load() || l.Leads()
}

// MayAct blocks until this copy may act, and answers true, or answers
// false when stop ends first or this process has stepped down. A copy
// that acted without an election and then found another holder waits
// here until it takes the Lease. An
// operator whose passes can stop acting between two passes, which only
// ActWhenRefused allows, calls it before each pass.
func (l *Lead) MayAct(stop context.Context) bool {
	for !l.acting() {
		if l.stepping.Load() {
			return false
		}
		select {
		case <-l.started:
		case <-l.modeChanged:
		case <-stop.Done():
			return false
		}
	}
	return stop.Err() == nil
}
