// Package election keeps one acting copy of a singleton operator.
//
// A singleton operator creates and deletes objects and writes status
// from one view of the cluster. Two copies that act at once race: both
// create a pod for one object, or one deletes the pod the other just
// made, or two status writes overwrite each other. A rolling update, a
// node partition, and a replica count above one each run a second copy,
// so every singleton operator elects one copy with a coordination.k8s.io
// Lease, through this package.
//
// The election is client-go's leaderelection package with a LeaseLock.
// Only the copy that holds the Lease acts. Every other copy waits and
// reads the Lease, so a rolling update starts the new pod beside the old
// one, and the new pod takes over when the old one releases the Lease.
//
// This package adds four things to client-go's election:
//
//   - A second release after client-go's, which survives a renewal that
//     lands late (Lead.end).
//   - The exit of the process when the Lease is lost.
//   - The take of an expired Lease that an earlier process of the same
//     pod held (renewalClock.Get).
//   - A write guard for the operator's client (Lead.MayWrite).
//
// The election is not fencing. A leader that pauses, for example on a
// stalled node, can resume after its Lease expired and finish a request
// it had already sent. A leader that runs stops writing, and exits,
// before another copy can take the Lease, because of the timings below
// and the write guard.
//
// The package links client-go's typed clientset for the coordination
// group. A program that must stay small, such as the pod build of
// media-operator, never imports it.
package election

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// The election's three durations set the Lease's duration to 30
// seconds, twice client-go's default, with client-go's 10-second
// renewal deadline and a 5-second retry period in place of its 2
// seconds.
//
// The retry period sets the load. The leader renews once per retry
// period, and a waiting copy reads the Lease once every 5 to 11
// seconds, because client-go adds up to 1.2 retry periods of jitter. A
// cluster can already have several clients that renew a Lease every one
// or two seconds, and an operator does not need faster failover.
//
// The duration sets the safety margin. After its last renewal at T, the
// leader tries again at T+5s, and gives up at T+15s when the renewal
// deadline passes. client-go then releases the Lease, bounded by one
// more renewal deadline, before it calls OnStoppedLeading, so the
// leader exits by T+25s. A waiting copy takes the Lease 30 seconds
// after it saw the last renewal, which is after T+30s. With client-go's
// 15-second duration, the two times would meet.
//
// The write guard is stricter than the elector. It refuses a write once
// the last renewal is one renewal deadline old, at T+10s. A write it
// allowed is abandoned by the client's request timeout, RequestTimeout,
// by T+25s, before a new leader can start at T+30s.
//
// The cost is failover time. A waiting copy takes a released Lease on
// its next read, within about 11 seconds, and an abandoned Lease 30 to
// 41 seconds after the last renewal.
const (
	Duration      = 30 * time.Second
	RenewDeadline = 10 * time.Second
	RetryPeriod   = 5 * time.Second
)

// RequestTimeout is the longest request the client of an elected
// operator may send: pass it as apiclient.InClusterOptions.Timeout. A
// write that the guard allowed just before T+10s must end before a new
// leader can start at T+30s, less one retry period of margin, so it
// ends by T+25s. apiclient's default of 30 seconds is too long.
const RequestTimeout = Duration - RenewDeadline - RetryPeriod

// Options name the Lease and the process that competes for it.
type Options struct {
	// Name and Namespace name the Lease.
	Name      string
	Namespace string
	// Pod is the name of the pod this process runs in. The identity the
	// Lease names is the pod's name and a random suffix, so `kubectl get
	// lease` shows which pod leads.
	Pod string

	// Report receives each line the election logs. The operator adds
	// its own prefix and writes the line where its log goes.
	Report func(line string)
	// Exit ends the process after a lost Lease, with code 1. It is
	// os.Exit unless a test sets it.
	Exit func(code int)

	// ActWhenRefused makes the copy act without an election while the
	// API server refuses the Lease (unelected.go). Only liken's cluster
	// operator sets it, for the reason unelected.go gives. Unelected
	// receives the mode each time it changes.
	ActWhenRefused bool
	Unelected      func(bool)
}

// Lead is this process's part in the election.
type Lead struct {
	options  Options
	elector  *leaderelection.LeaderElector
	identity string
	lock     *renewalClock

	// leases reaches the Lease itself, for the release that follows
	// client-go's own. end says why.
	leases coordinationv1.LeasesGetter

	// started closes when this process takes the Lease, and done closes
	// when the election ends.
	started chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc

	// stepping marks an end this process chose, a shutdown, so the end
	// of the election is not a loss.
	stepping atomic.Bool

	// unelected is true while this copy acts without an election.
	// modeChanged receives a signal each time it flips, and when the
	// lead starts.
	unelected   atomic.Bool
	modeChanged chan struct{}
}

// New builds the election. The identity is the pod's name and a random
// suffix, so a restarted container is a new candidate.
func New(config *rest.Config, options Options) (*Lead, error) {
	if options.Name == "" || options.Namespace == "" || options.Pod == "" {
		return nil, errors.New("the election needs the Lease's name and namespace and the pod's name")
	}
	if options.Report == nil {
		options.Report = func(string) {}
	}
	if options.Exit == nil {
		options.Exit = os.Exit
	}
	leases, err := coordinationv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	l := &Lead{
		options:     options,
		identity:    options.Pod + "_" + hex.EncodeToString(suffix),
		leases:      leases,
		started:     make(chan struct{}),
		done:        make(chan struct{}),
		modeChanged: make(chan struct{}, 1),
	}
	l.lock = &renewalClock{
		Interface: &resourcelock.LeaseLock{
			LeaseMeta:  metav1.ObjectMeta{Name: options.Name, Namespace: options.Namespace},
			Client:     leases,
			LockConfig: resourcelock.ResourceLockConfig{Identity: l.identity},
		},
		pod:  options.Pod + "_",
		lead: l,
	}
	report := options.Report
	subject := l.subject()
	l.elector, err = leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          l.lock,
		LeaseDuration: Duration,
		RenewDeadline: RenewDeadline,
		RetryPeriod:   RetryPeriod,
		// The release writes the Lease with no holder when the election's
		// context ends, so a waiting copy takes it on its next retry
		// instead of after the Lease's duration. end follows it with a
		// release of its own, for the case where client-go's fails.
		ReleaseOnCancel: true,
		Name:            options.Name,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(context.Context) {
				report(fmt.Sprintf("holding %s as %s", subject, l.identity))
				close(l.started)
				l.leaseAnswered(false)
				l.signalMode()
			},
			OnNewLeader: func(holder string) {
				if holder != "" && holder != l.identity {
					report(fmt.Sprintf("waiting for %s, held by %s", subject, holder))
				}
			},
			// client-go calls this whenever the election ends. A loss ends
			// the process at once, because a pass in flight must not keep
			// writing after another copy takes the Lease. The kubelet
			// restarts the container, and it competes as a new candidate.
			OnStoppedLeading: func() {
				if l.stepping.Load() {
					return
				}
				report(fmt.Sprintf("lost %s", subject))
				l.options.Exit(1)
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return l, nil
}

// ErrStopped says that stop ended before this process could act. A copy
// that never led has nothing to release, so the operator exits cleanly.
var ErrStopped = errors.New("the stop came before the Lease")

// Start builds the election, runs it, and blocks until this process may
// act. It answers ErrStopped when stop ends first. Every singleton
// operator starts its election this way, with the configuration of its
// pod's service account.
func Start(stop context.Context, config *rest.Config, options Options) (*Lead, error) {
	l, err := New(config, options)
	if err != nil {
		return nil, err
	}
	l.Run()
	if !l.Await(stop) {
		return nil, ErrStopped
	}
	return l, nil
}

// Identity is the name this process holds the Lease under.
func (l *Lead) Identity() string { return l.identity }

func (l *Lead) subject() string {
	return "Lease " + l.options.Namespace + "/" + l.options.Name
}

// Run starts the election in the background.
func (l *Lead) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	go func() {
		l.elector.Run(ctx)
		close(l.done)
	}()
}

// Await returns true when this process may act: it holds the Lease, or
// it acts without an election (unelected.go). It returns false when stop
// ends first. After a false return the election has ended and this
// process holds nothing.
func (l *Lead) Await(stop context.Context) bool {
	if l.MayAct(stop) {
		return true
	}
	l.end()
	return false
}

// Leads answers whether the elector has started this process's lead.
func (l *Lead) Leads() bool {
	select {
	case <-l.started:
		return true
	default:
		return false
	}
}

// ErrNotLeading refuses a write while this process cannot show that it
// holds the Lease.
var ErrNotLeading = errors.New("this copy does not hold a current leader Lease")

// MayWrite is the client's write guard: pass it to
// apiclient.Client.WithWriteGuard, on a client whose requests end
// within RequestTimeout. It allows a write only while this
// process leads and its last renewal is younger than one renewal
// deadline. The elector itself gives up only after the renewal deadline
// passes with no success, which is up to one retry period later, so the
// guard stops writes first.
func (l *Lead) MayWrite() error {
	if l.unelected.Load() && !l.stepping.Load() {
		return nil
	}
	if !l.Leads() || l.stepping.Load() {
		return ErrNotLeading
	}
	if age := time.Since(l.lock.lastRenewal()); age >= RenewDeadline {
		return fmt.Errorf("%w: the last renewal was %s ago", ErrNotLeading, age.Round(time.Second))
	}
	return nil
}

// StepDown ends this process's lead on a shutdown, and releases the
// Lease. The caller calls it after its last write has returned, so
// nothing writes once the Lease is free. A caller that cannot show
// that, because a writer did not stop in time, does not call StepDown:
// the process exits holding the Lease, and a waiting copy takes it when
// it expires, after any write still in flight can land.
func (l *Lead) StepDown() {
	l.end()
}

// end stops the election and waits for the release. The kubelet kills
// the container at the end of its grace period. client-go bounds its
// release by the renewal deadline, and clearIfHeld is bounded by half of
// it, so the wait ends well before that.
//
// client-go's release can fail on a normal shutdown. The cancel ends
// the renewal loop, but a renewal that was already sent can still reach
// the API server after the release read the Lease. The release's update
// then carries a stale resourceVersion, the API server refuses it with a
// conflict, and a waiting copy waits out the whole duration. So once the
// election has ended, end reads the Lease again and clears it itself if
// it still names this process. The late renewal can land after this
// read too, so a conflict reads the Lease again. The renewal loop sends
// one renewal at a time, so at most one late write is in flight.
func (l *Lead) end() {
	l.stepping.Store(true)
	l.cancel()
	select {
	case <-l.done:
	case <-time.After(RenewDeadline + time.Second):
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), RenewDeadline/2)
	defer cancel()
	l.clearIfHeld(ctx)
}

// clearIfHeld writes the Lease with no holder when it still names this
// process, or an earlier process of this pod whose Lease has expired
// (renewalClock.Get says why that one is safe to clear). A stop can
// arrive before the elector's next read takes such a Lease, and without
// this clear a copy in another pod waits out the whole duration. It
// reads the Lease again after a conflict, because the write it lost to
// can be this process's own late renewal. A Lease that names another
// process, or no process, is left alone.
func (l *Lead) clearIfHeld(ctx context.Context) {
	leases := l.leases.Leases(l.options.Namespace)
	subject := l.subject()
	for range 3 {
		lease, err := leases.Get(ctx, l.options.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				l.options.Report(fmt.Sprintf("reading %s to release it: %v", subject, err))
			}
			return
		}
		if !l.mayClear(lease) {
			return
		}
		none, released, second := "", metav1.NewMicroTime(time.Now()), int32(1)
		lease.Spec.HolderIdentity = &none
		lease.Spec.LeaseDurationSeconds = &second
		lease.Spec.RenewTime = &released
		_, err = leases.Update(ctx, lease, metav1.UpdateOptions{})
		if err == nil {
			l.options.Report(fmt.Sprintf("released %s", subject))
			return
		}
		if !apierrors.IsConflict(err) {
			l.options.Report(fmt.Sprintf("releasing %s: %v", subject, err))
			return
		}
	}
}

// mayClear answers whether clearIfHeld may write the Lease with no
// holder.
func (l *Lead) mayClear(lease *coordination.Lease) bool {
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return false
	}
	holder := *lease.Spec.HolderIdentity
	if holder == l.identity {
		return true
	}
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return false
	}
	return l.lock.abandonedByThisPod(holder, lease.Spec.RenewTime.Time,
		int(*lease.Spec.LeaseDurationSeconds), time.Now())
}
