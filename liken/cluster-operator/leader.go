package main

// Leader election: only one copy of this program acts on the fleet.
//
// The sweep writes Lost verdicts, the Cluster's status, and reboot
// grants, and it evicts and deletes. Two copies that sweep at the same
// time can each grant a reboot turn from a different view of the
// fleet, and together exceed the disruption budget. A rolling update, a
// node partition, and a replica count above one each run a second
// copy, so this program keeps one acting copy in its own code, with
// leader election on a coordination.k8s.io Lease named
// liken-cluster-operator in liken-system.
//
// The election is client-go's leaderelection package with a LeaseLock.
// Only the copy that holds the Lease opens its watches and sweeps, with
// one exception: a copy that the API server refuses on the Lease acts
// without an election (unelected.go). Every other copy waits, so a
// rolling update starts the new pod beside the old one, and the new pod
// takes over when the old one releases the Lease.
//
// The election is not fencing. A leader that pauses, for example on a
// stalled node, can resume after its Lease expired and finish a
// request it had already sent. A leader that runs stops writing, and
// exits, before another copy can take the Lease, because of the
// timings below and the write guard (mayWrite).
//
// This file links client-go's typed clientset, through the
// leaderelection package. That costs this binary about 13 MB, and it
// is accepted here because the cluster operator runs as one pod in the
// fleet. The machine operator runs on every machine
// and never imports this package.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

// leaseName names the Lease the copies compete for, and leaseNamespace
// is the namespace the whole OS uses.
const (
	leaseName      = "liken-cluster-operator"
	leaseNamespace = "liken-system"
)

// leaseTiming holds the election's three durations.
type leaseTiming struct {
	duration      time.Duration
	renewDeadline time.Duration
	retryPeriod   time.Duration
}

// operatorLeaseTiming sets the Lease's duration to 30 seconds, twice
// client-go's default, with client-go's 10-second renewal deadline and
// a 5-second retry period in place of its 2 seconds.
//
// The retry period sets the load. The leader renews once per retry
// period, and a waiting copy reads the Lease once every 5 to 11
// seconds, because client-go adds up to 1.2 retry periods of jitter.
//
// The duration sets the safety margin. After its last renewal at T,
// the leader tries again at T+5s, and gives up at T+15s when the
// renewal deadline passes. client-go then releases the Lease, bounded
// by one more renewal deadline, before it calls OnStoppedLeading, so
// the leader exits by T+25s. A waiting copy takes the Lease 30 seconds
// after it saw the last renewal, which is after T+30s. With client-go's
// 15-second duration, the two times would meet.
//
// The write guard is stricter than the elector. It refuses a write
// once the last renewal is one renewal deadline old, at T+10s. A write
// it allowed is abandoned by the client's 15-second request timeout
// (kubernetes/apiclient.go) by T+25s, before a new leader can start at
// T+30s.
//
// The cost is failover time. A waiting copy takes a released Lease on
// its next read, within about 11 seconds, and an abandoned Lease 30 to
// 41 seconds after the last renewal.
var operatorLeaseTiming = leaseTiming{
	duration:      30 * time.Second,
	renewDeadline: 10 * time.Second,
	retryPeriod:   5 * time.Second,
}

// leadership is this process's part in the election.
type leadership struct {
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

	// unelected is true while this copy acts without an election,
	// because the API server refuses its Lease (unelected.go).
	// modeChanged receives a signal each time it flips, and when the
	// lead starts. setUnelected publishes the flag as a gauge.
	unelected    atomic.Bool
	modeChanged  chan struct{}
	setUnelected func(bool)

	// exit ends the process. It is a field so a test reads the code
	// instead of ending the test binary.
	exit   func(code int)
	report func(line string)
}

// newLeadership builds the election. The identity is the pod's name and
// a random suffix, so a restarted container is a new candidate and
// waits for the Lease its earlier process held to expire
// (renewalClock.Get).
func newLeadership(config *rest.Config, pod string,
	setUnelected func(bool), exit func(int), report func(string)) (*leadership, error) {
	leases, err := coordinationv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	l := &leadership{
		identity:     pod + "_" + hex.EncodeToString(suffix),
		leases:       leases,
		started:      make(chan struct{}),
		done:         make(chan struct{}),
		modeChanged:  make(chan struct{}, 1),
		setUnelected: setUnelected,
		exit:         exit,
		report:       report,
	}
	l.lock = &renewalClock{
		Interface: &resourcelock.LeaseLock{
			LeaseMeta:  metav1.ObjectMeta{Name: leaseName, Namespace: leaseNamespace},
			Client:     leases,
			LockConfig: resourcelock.ResourceLockConfig{Identity: l.identity},
		},
		pod:      pod + "_",
		answered: l.leaseAnswered,
		leading:  l.leads,
	}
	subject := "lease " + leaseNamespace + "/" + leaseName
	l.elector, err = leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          l.lock,
		LeaseDuration: operatorLeaseTiming.duration,
		RenewDeadline: operatorLeaseTiming.renewDeadline,
		RetryPeriod:   operatorLeaseTiming.retryPeriod,
		// The release writes the Lease with no holder when the election's
		// context ends, so a waiting copy takes it on its next retry
		// instead of after the Lease's duration. end follows it with a
		// release of its own, for the case where client-go's fails.
		ReleaseOnCancel: true,
		Name:            leaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(context.Context) {
				report(fmt.Sprintf("%s: holding %s as %s", component, subject, l.identity))
				close(l.started)
				l.leaseAnswered(false)
				l.signalMode()
			},
			OnNewLeader: func(holder string) {
				if holder != "" && holder != l.identity {
					report(fmt.Sprintf("%s: waiting for %s, held by %s", component, subject, holder))
				}
			},
			// client-go calls this whenever the election ends. A loss ends
			// the process at once, because a sweep in flight must not keep
			// writing after another copy takes the Lease. The kubelet
			// restarts the container, and it waits as a new candidate.
			OnStoppedLeading: func() {
				if l.stepping.Load() {
					return
				}
				report(fmt.Sprintf("%s: lost %s", component, subject))
				l.exit(1)
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return l, nil
}

// renewalClock is the Lease lock with one addition: it records when
// this process last sent a write of the Lease that the API server
// accepted with this process as the holder. The time is taken before
// the request is sent, so it is never later than the time a waiting
// copy first reads the renewal and measures the duration from.
type renewalClock struct {
	resourcelock.Interface

	mu      sync.Mutex
	renewed time.Time

	// pod is the prefix that every identity of this pod starts with.
	// The pod's name is a DNS subdomain, which has no underscore, so
	// the prefix names this pod and no other.
	pod string

	// answered and leading serve the refusal check in unelected.go.
	answered func(refused bool)
	leading  func() bool
}

func (r *renewalClock) Create(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	sent := time.Now()
	err := r.Interface.Create(ctx, record)
	r.mark(sent, record, err)
	r.classifyWrite(err, true)
	return err
}

func (r *renewalClock) Update(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	sent := time.Now()
	err := r.Interface.Update(ctx, record)
	r.mark(sent, record, err)
	r.classifyWrite(err, false)
	return err
}

// Get reads the Lease, and shows client-go a Lease with no holder when
// an earlier process of this pod held it and its last renewal is one
// Lease duration old.
//
// client-go does not compare renewTime with its own clock, because the
// holder can run on another node with another clock. It measures the
// duration from the time it first read the current record. A process
// that restarts, or that starts after the API server comes back, first
// reads its earlier process's Lease late, and waits a whole duration
// from that read. When the only API server reboots, the leader cannot
// renew, exits, and restarts, and the fleet then has no acting copy
// for a whole Lease duration after the API server returns. A rolling
// update in that time moves the wait to the new pod, which cannot
// clear a Lease that names another process.
//
// An earlier process of this pod is not a paused leader that can
// resume. The kubelet starts a container's process again only after
// the one before it ended, so that process sends no more writes. It
// wrote renewTime from the clock of this node, so the comparison with
// this process's clock is sound.
//
// The Lease that client-go then updates keeps the resourceVersion of
// this read, so the take is a conditional write, and a conflict with
// any other writer ends it.
func (r *renewalClock) Get(ctx context.Context) (*resourcelock.LeaderElectionRecord, []byte, error) {
	record, raw, err := r.Interface.Get(ctx)
	r.classifyRead(record, err)
	if err == nil && r.abandonedByThisPod(record.HolderIdentity, record.RenewTime.Time,
		record.LeaseDurationSeconds, time.Now()) {
		free := *record
		free.HolderIdentity = ""
		return &free, raw, nil
	}
	return record, raw, err
}

// abandonedByThisPod answers whether holder is an earlier process of
// this pod, whose last renewal at renewed is at least the Lease's
// duration old at now.
func (r *renewalClock) abandonedByThisPod(holder string, renewed time.Time, seconds int, now time.Time) bool {
	if holder == r.Identity() || !strings.HasPrefix(holder, r.pod) {
		return false
	}
	return !now.Before(renewed.Add(time.Duration(seconds) * time.Second))
}

func (r *renewalClock) mark(sent time.Time, record resourcelock.LeaderElectionRecord, err error) {
	if err != nil || record.HolderIdentity != r.Identity() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewed = sent
}

func (r *renewalClock) lastRenewal() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.renewed
}

// errNotLeading refuses a write while this process cannot show that it
// holds the Lease.
var errNotLeading = errors.New("this copy of the cluster operator does not hold a current leader lease")

// mayWrite is the client's write guard. It allows a write only while
// this process leads and its last renewal is younger than one renewal
// deadline. The elector itself gives up only after the renewal deadline
// passes with no success, which is up to one retry period later, so the
// guard stops writes first. operatorLeaseTiming gives the numbers.
func (l *leadership) mayWrite() error {
	if l.unelected.Load() && !l.stepping.Load() {
		return nil
	}
	select {
	case <-l.started:
	default:
		return errNotLeading
	}
	if l.stepping.Load() {
		return errNotLeading
	}
	if age := time.Since(l.lock.lastRenewal()); age >= operatorLeaseTiming.renewDeadline {
		return fmt.Errorf("%w: the last renewal was %s ago", errNotLeading, age.Round(time.Second))
	}
	return nil
}

// run starts the election in the background.
func (l *leadership) run() {
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	go func() {
		l.elector.Run(ctx)
		close(l.done)
	}()
}

// await returns true when this process may act: it holds the Lease, or
// the API server refuses the Lease and it acts without an election
// (unelected.go). It returns false when stop ends first. After a false
// return the election has ended and this process holds nothing.
func (l *leadership) await(stop context.Context) bool {
	if l.mayAct(stop) {
		return true
	}
	l.end()
	return false
}

// stepDown ends this process's lead on a shutdown. The caller calls it
// after the last sweep has returned. The sweep is the only part of
// this program that writes, so nothing can write once it has returned,
// and the Lease is released at once.
func (l *leadership) stepDown() {
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
func (l *leadership) end() {
	l.stepping.Store(true)
	l.cancel()
	select {
	case <-l.done:
	case <-time.After(operatorLeaseTiming.renewDeadline + time.Second):
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), operatorLeaseTiming.renewDeadline/2)
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
func (l *leadership) clearIfHeld(ctx context.Context) {
	leases := l.leases.Leases(leaseNamespace)
	for range 3 {
		lease, err := leases.Get(ctx, leaseName, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				l.report(fmt.Sprintf("%s: reading lease %s/%s to release it: %v", component, leaseNamespace, leaseName, err))
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
			l.report(fmt.Sprintf("%s: released lease %s/%s", component, leaseNamespace, leaseName))
			return
		}
		if !apierrors.IsConflict(err) {
			l.report(fmt.Sprintf("%s: releasing lease %s/%s: %v", component, leaseNamespace, leaseName, err))
			return
		}
	}
}

// mayClear answers whether clearIfHeld may write the Lease with no
// holder.
func (l *leadership) mayClear(lease *coordination.Lease) bool {
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

// lead blocks until this process holds the Lease. It exits the process
// when stop ends first, and whenever the Lease is lost after that. The
// pod's hostname is its name, which makes the identity readable in
// `kubectl get lease`.
func lead(stop context.Context, setUnelected func(bool)) *leadership {
	pod, err := os.Hostname()
	if err != nil {
		fatal("reading the pod's name for the leader election: %v", err)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		fatal("in-cluster config for the leader election: %v", err)
	}
	l, err := newLeadership(config, pod, setUnelected, os.Exit,
		func(line string) { fmt.Println(line) })
	if err != nil {
		fatal("leader election: %v", err)
	}
	l.run()
	if !l.await(stop) {
		os.Exit(0)
	}
	return l
}
