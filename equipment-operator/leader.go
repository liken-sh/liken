//go:build !node

package main

// The Deployment's operator is a cluster singleton. It holds one
// session on each receiver, creates and deletes the Receivers and
// Televisions discovery owns, and writes their status, so two copies
// that act at once race: both open a session on one receiver and send
// it commands, or both create one Television. The Deployment runs one
// replica with the Recreate strategy, and a node partition or a
// replica count above one can still run a second copy. So the operator
// enforces one acting copy in its own code, with leader election on a
// coordination.k8s.io Lease.
//
// The election is client-go's leaderelection package with a LeaseLock.
// Only the copy that holds the Lease binds its metrics port, searches
// for devices, opens a session on a receiver, watches the API server,
// or writes to it. Every other copy sends the API server only its
// reads of the Lease, and the create of the Lease when none exists.
//
// The election is not fencing. A leader that pauses, for example under
// a stalled node, can resume after its Lease expired and finish a
// request it had already sent. A leader that runs notices a loss and
// exits before another copy can take the Lease, because of the timings
// below.
//
// This file links client-go's typed clientset, which the election
// needs and which more than doubles the program. The node workload's
// build, with the build tag node, leaves this file out, and
// leader_node.go says why.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// leaseName names the Lease the operator's copies compete for, in the
// operator's own namespace.
const leaseName = "equipment-operator"

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
// The duration sets the safety margin. After its last renewal at T, the
// leader tries again at T+5s, and gives up at T+15s when the renewal
// deadline passes. client-go then releases the Lease, bounded by one
// more renewal deadline, before it calls OnStoppedLeading, so the
// leader exits by T+25s. A waiting copy takes the Lease 30 seconds
// after it saw the last renewal, which is after T+30s. With client-go's
// 15-second duration, a new leader could start while the old one can
// still write.
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
	timing   leaseTiming

	// leases and namespace reach the Lease itself, for the release that
	// follows client-go's own. end says why.
	leases    coordinationv1.LeasesGetter
	namespace string

	// started closes when this process takes the Lease, and done closes
	// when the election ends.
	started chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc

	// stepping marks an end this process chose, a shutdown, so the end
	// of the election is not a loss.
	stepping atomic.Bool

	// exit ends the process. It is a field so a test reads the code
	// instead of ending the test binary.
	exit   func(code int)
	report func(line string)
}

// newLeadership builds the election. The identity is the pod's name and
// a random suffix, so a restarted container is a new candidate and
// waits for the Lease its earlier process held.
func newLeadership(config *rest.Config, namespace, pod string, timing leaseTiming,
	exit func(int), report func(string)) (*leadership, error) {
	leases, err := coordinationv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	l := &leadership{
		identity:  pod + "_" + hex.EncodeToString(suffix),
		timing:    timing,
		leases:    leases,
		namespace: namespace,
		started:   make(chan struct{}),
		done:      make(chan struct{}),
		exit:      exit,
		report:    report,
	}
	subject := "Lease " + namespace + "/" + leaseName
	l.elector, err = leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			LeaseMeta:  metav1.ObjectMeta{Name: leaseName, Namespace: namespace},
			Client:     leases,
			LockConfig: resourcelock.ResourceLockConfig{Identity: l.identity},
		},
		LeaseDuration: timing.duration,
		RenewDeadline: timing.renewDeadline,
		RetryPeriod:   timing.retryPeriod,
		// The release writes the Lease with no holder when the election's
		// context ends, so a waiting copy takes it on its next retry
		// instead of after the Lease's duration. end follows it with a
		// release of its own, for the case where client-go's fails.
		ReleaseOnCancel: true,
		Name:            leaseName,
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
			// client-go calls this whenever the election ends. A loss ends
			// the process at once, because a pass or a receiver session in
			// flight must not keep writing after another copy takes the
			// Lease. The kubelet restarts the container, and it waits as a
			// new candidate.
			OnStoppedLeading: func() {
				if l.stepping.Load() {
					return
				}
				report(fmt.Sprintf("lost %s", subject))
				l.exit(1)
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return l, nil
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

// await returns true when this process holds the Lease, and false when
// stop ends first. After a false return the election has ended and
// this process holds nothing.
func (l *leadership) await(stop context.Context) bool {
	select {
	case <-l.started:
		return true
	case <-stop.Done():
		l.end()
		return false
	}
}

// actWhileLeading waits for the Lease, runs act, and releases the Lease
// after act returns. act is everything the operator does: it binds the
// metrics port, searches for devices, opens the receiver sessions, and
// watches and writes the API. So a copy that waits does none of that;
// it only reads the Lease. act returns on a shutdown after
// every part of it that writes has stopped, so the release comes after
// the last write. When act answers errStillWriting, a write can still
// be in flight, so the Lease is not released: the process exits
// holding it, and a waiting copy takes it when it expires. led says
// whether this process took the Lease before stop ended.
func (l *leadership) actWhileLeading(stop context.Context, act func() error) (led bool, err error) {
	if !l.await(stop) {
		return false, nil
	}
	err = act()
	if errors.Is(err, errStillWriting) {
		return true, err
	}
	l.end()
	return true, err
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
	case <-time.After(l.timing.renewDeadline + time.Second):
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), l.timing.renewDeadline/2)
	defer cancel()
	l.clearIfHeld(ctx)
}

// clearIfHeld writes the Lease with no holder when it still names this
// process. It reads the Lease again after a conflict, because the write
// it lost to can be this process's own late renewal. A Lease that names
// another process, or no process, is left alone.
func (l *leadership) clearIfHeld(ctx context.Context) {
	leases := l.leases.Leases(l.namespace)
	for range 3 {
		lease, err := leases.Get(ctx, leaseName, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				l.report(fmt.Sprintf("reading Lease %s/%s to release it: %v", l.namespace, leaseName, err))
			}
			return
		}
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != l.identity {
			return
		}
		none, released, second := "", metav1.NewMicroTime(time.Now()), int32(1)
		lease.Spec.HolderIdentity = &none
		lease.Spec.LeaseDurationSeconds = &second
		lease.Spec.RenewTime = &released
		_, err = leases.Update(ctx, lease, metav1.UpdateOptions{})
		if err == nil {
			l.report(fmt.Sprintf("released Lease %s/%s", l.namespace, leaseName))
			return
		}
		if !apierrors.IsConflict(err) {
			l.report(fmt.Sprintf("releasing Lease %s/%s: %v", l.namespace, leaseName, err))
			return
		}
	}
}

// lead starts the election for this pod. It exits the process when the
// pod does not state its name and namespace, which name the Lease and
// the identity it is held under.
func lead(client *Client, config settings) *leadership {
	if config.namespace == "" || config.pod == "" {
		fmt.Fprintf(os.Stderr, "%s and %s are unset, so the operator cannot name its Lease\n",
			podNamespaceVariable, podNameVariable)
		os.Exit(1)
	}
	l, err := newLeadership(client.restConfig(), config.namespace, config.pod, operatorLeaseTiming, os.Exit,
		func(line string) { fmt.Fprintln(os.Stderr, line) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "leader election: %v\n", err)
		os.Exit(1)
	}
	l.run()
	return l
}
