// Package election lets one replica of a singleton operator act at a
// time, by competing for a coordination.k8s.io Lease.
//
// A singleton operator creates and deletes objects and writes status
// from one view of the cluster. Two replicas acting at once race: both
// create a pod for one object, one deletes the pod the other just
// created, or their status writes overwrite each other. A rolling
// update, a node partition, or a replica count above one can each run a
// second replica, so every singleton operator elects its acting replica
// through this package.
//
// The election is client-go's leaderelection package with a LeaseLock.
// Only the replica that holds the Lease acts. The other replicas read
// the Lease and wait, so in a rolling update the new pod starts beside
// the old one and takes the Lease when the old one releases it.
//
// The package adds to client-go's election:
//
//   - Lead.end releases the Lease again after client-go's own release,
//     because client-go's release gives up on some errors.
//   - The process exits when it loses the Lease.
//   - renewalRecordingLock.Get lets a restarted process take the expired
//     Lease of an earlier process in the same pod, without waiting out
//     the Lease duration.
//   - Lead.MayWrite is a write guard for the operator's API client.
//
// The election is not fencing. A leader that pauses, for example on a
// stalled node, can resume after its Lease expired and finish a request
// it had already sent. A running leader stops sending writes and exits
// before another replica can take the Lease, but the API server can
// still commit a write after that (RequestTimeout explains). So a write
// that must not be committed late has to be conditional on something
// the next leader changes, such as the resourceVersion of the object it
// writes.
//
// The package links client-go's typed clientset for the coordination
// group, so a program that must stay small, such as the pod build of
// media-operator, does not import it.
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

// The durations are chosen so that a leader stops writing and exits
// before a waiting replica can take the Lease. The Lease duration is 30
// seconds, twice client-go's default of 15. The renewal deadline is
// client-go's default of 10 seconds. The retry period is 5 seconds,
// where client-go's default is 2.
//
// The retry period sets the load on the API server. The leader renews
// once per retry period, and a waiting replica reads the Lease every 5
// to 11 seconds, because client-go adds up to 1.2 retry periods of
// jitter. A cluster can already have several clients that renew a Lease
// every one or two seconds, and an operator does not need faster
// failover.
//
// The Lease duration decides how long the old leader has to exit.
// Say the leader last renewed at T. It
// tries again at T+5s, and gives up at T+15s when the renewal deadline
// passes. client-go then releases the Lease, which takes at most one
// more renewal deadline, and calls OnStoppedLeading, so the leader
// exits by T+25s. A waiting replica takes the Lease 30 seconds after it
// first saw the last renewal, which is after T+30s. With client-go's
// 15-second duration, a waiting replica could take the Lease at T+15s,
// while the old leader still runs.
//
// The cost is failover time. A waiting replica takes a released Lease
// on its next read, within about 11 seconds, and an abandoned Lease 30
// to 41 seconds after the last renewal.
const (
	Duration      = 30 * time.Second
	RenewDeadline = 10 * time.Second
	RetryPeriod   = 5 * time.Second
)

// RequestTimeout is the longest that a request from an elected
// operator's client may take. Pass it as
// apiclient.InClusterOptions.Timeout. When the last renewal was at T,
// the write guard can allow a write just before T+10s, and with this
// timeout the client abandons it by T+25s, before a new leader can
// start at T+30s. apiclient's default timeout of 30 seconds is too
// long for that.
//
// The timeout ends the client's wait, not the write. kube-apiserver
// keeps running a request's handler after the request times out, and
// counts each such handler in apiserver_request_post_timeout_total, so
// the API server can commit a write after the client gave up on it and
// after a new leader took the Lease.
const RequestTimeout = Duration - RenewDeadline - RetryPeriod

// Options configures an election: the Lease to compete for, and the
// process that competes for it.
type Options struct {
	// Name and Namespace identify the Lease.
	Name      string
	Namespace string
	// Pod is the name of the pod this process runs in. The identity
	// in the Lease starts with it, so `kubectl get lease` shows
	// which pod leads.
	Pod string

	// Report receives each line the election logs.
	Report func(line string)
	// Exit ends the process with code 1 after the Lease is lost. It
	// defaults to os.Exit.
	Exit func(code int)
}

// Lead is one process's participation in the election for a Lease.
type Lead struct {
	options  Options
	elector  *leaderelection.LeaderElector
	identity string
	lock     *renewalRecordingLock

	// leases is the client that clearIfHeld reads and writes the
	// Lease with.
	leases coordinationv1.LeasesGetter

	// started closes when this process takes the Lease, and done
	// closes when the election ends.
	started chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc

	// stepping is set when this process ends the election on
	// purpose, so that OnStoppedLeading does not treat the end as a
	// loss.
	stepping atomic.Bool
}

// New builds an election for the Lease. The identity is the pod name
// plus a random suffix, so a restarted container competes as a new
// candidate.
func New(config *rest.Config, options Options) (*Lead, error) {
	if options.Name == "" || options.Namespace == "" || options.Pod == "" {
		return nil, errors.New("Options.Name, Options.Namespace, and Options.Pod must all be set")
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
		options:  options,
		identity: options.Pod + "_" + hex.EncodeToString(suffix),
		leases:   leases,
		started:  make(chan struct{}),
		done:     make(chan struct{}),
	}
	l.lock = &renewalRecordingLock{
		Interface: &resourcelock.LeaseLock{
			LeaseMeta:  metav1.ObjectMeta{Name: options.Name, Namespace: options.Namespace},
			Client:     leases,
			LockConfig: resourcelock.ResourceLockConfig{Identity: l.identity},
		},
		pod: options.Pod + "_",
	}
	report := options.Report
	subject := l.subject()
	l.elector, err = leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          l.lock,
		LeaseDuration: Duration,
		RenewDeadline: RenewDeadline,
		RetryPeriod:   RetryPeriod,
		// ReleaseOnCancel makes client-go write the Lease with
		// no holder when the election's context ends, so a
		// waiting replica takes it on its next read and does
		// not wait out the Lease duration.
		ReleaseOnCancel: true,
		Name:            options.Name,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(context.Context) {
				report(fmt.Sprintf("holding %s as %s", subject, l.identity))
				close(l.started)
			},
			OnNewLeader: func(holder string) {
				if holder != "" && holder != l.identity {
					report(fmt.Sprintf("waiting for %s, held by %s", subject, holder))
				}
			},
			// client-go calls this whenever the election
			// ends. Unless this process ended it on
			// purpose, the Lease is lost, and the process
			// exits at once so that a reconcile pass in
			// progress cannot keep writing after another
			// replica takes the Lease. The kubelet then
			// restarts the container.
			OnStoppedLeading: func() {
				if l.stepping.Load() {
					return
				}
				report(fmt.Sprintf("lost %s, exiting", subject))
				l.options.Exit(1)
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return l, nil
}

// ErrStopped is returned by Start when stop ends before this process
// takes the Lease. The process never led, so the operator can exit
// with no cleanup.
var ErrStopped = errors.New("the election was stopped before this process took the Lease")

// Start builds the election, runs it, and blocks until this process
// holds the Lease. It returns ErrStopped when stop ends first.
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

// Identity is the holder name that this process writes into the Lease.
func (l *Lead) Identity() string { return l.identity }

func (l *Lead) subject() string {
	return "Lease " + l.options.Namespace + "/" + l.options.Name
}

func (l *Lead) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	go func() {
		l.elector.Run(ctx)
		close(l.done)
	}()
}

// Await blocks until this process holds the Lease and returns true.
// When stop ends first, it ends the election and returns false.
//
// A failed request for the Lease, a 403 Forbidden included, does not
// end the wait, because client-go retries it.
func (l *Lead) Await(stop context.Context) bool {
	select {
	case <-l.started:
	case <-stop.Done():
	}
	if stop.Err() == nil {
		return true
	}
	l.end()
	return false
}

// Leads reports whether this process has taken the Lease. It does not
// become false afterwards, so call MayWrite before a write.
func (l *Lead) Leads() bool {
	select {
	case <-l.started:
		return true
	default:
		return false
	}
}

// ErrNotLeading is returned by MayWrite when it refuses a write.
var ErrNotLeading = errors.New("this process does not hold a current leader Lease")

// MayWrite is the write guard for the operator's API client. Pass it
// to apiclient.Client.WithWriteGuard, on a client whose requests time
// out within RequestTimeout. It allows a write only while this
// process leads, is not stepping down, and its last renewal is less
// than one renewal deadline old. So after a last renewal at T it
// refuses writes from T+10s, and the elector gives up later, at
// T+15s.
func (l *Lead) MayWrite() error {
	if !l.Leads() || l.stepping.Load() {
		return ErrNotLeading
	}
	if age := time.Since(l.lock.lastRenewal()); age >= RenewDeadline {
		return fmt.Errorf("%w: the last renewal was %s ago, and a write needs one less than %s old", ErrNotLeading, age.Round(time.Second), RenewDeadline)
	}
	return nil
}

// StepDown ends this process's lead on a shutdown and releases the
// Lease. Call it after the last write has returned, so that nothing
// writes once the Lease is free. When a writer did not stop in time,
// do not call StepDown. The process then exits holding the Lease, and
// a waiting replica takes it only when it expires, which gives a write
// still in progress time to finish.
func (l *Lead) StepDown() {
	l.end()
}

// end stops the election, waits for client-go's release, and then
// releases the Lease itself with clearIfHeld. client-go's release gives
// up on any error other than a conflict. It also releases only a Lease
// that this process took. When a stop comes after Get found the expired
// Lease of an earlier process of this pod, and before the elector took
// it, clearIfHeld clears that Lease too.
//
// end waits at most 16 seconds: 11 for client-go's release and 5 for
// clearIfHeld. The kubelet's default grace period of 30 seconds is
// longer, so the release finishes before the kubelet kills the
// container.
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

// clearIfHeld writes the Lease with no holder when it names this
// process, or an earlier process of this pod whose Lease has expired
// (renewalRecordingLock.Get explains why that one is safe to clear).
// Without this, a replica in another pod waits out the whole duration.
//
// After a conflict it reads the Lease again, because the write it lost
// to can be a renewal of this process that the API server committed
// after the elector stopped. The elector sends one renewal at a time,
// so at most one such write is pending.
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
