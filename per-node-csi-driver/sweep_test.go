package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// aVolume builds a PersistentVolume of this driver whose handle is its
// name.
func aVolume(handle string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: handle},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver: driverName, VolumeHandle: handle,
				},
			},
		},
	}
}

// copiesIn lists the handles the store holds, so a test reads what the
// sweep left.
func copiesIn(t *testing.T, answering *driver) []string {
	t.Helper()
	return answering.store.handles()
}

func TestTheSweepRemovesTheCopyOfAVolumeNoOneNamesAndNoPodHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		for _, handle := range []string{"example-store", "some-cache", "third-copy"} {
			if err := answering.store.makeCopy(handle); err != nil {
				t.Fatalf("makeCopy: %v", err)
			}
		}
		// A pod holds one of the two copies that no PersistentVolume names.
		if _, err := answering.NodePublishVolume(t.Context(),
			publishing("third-copy", filepath.Join(t.TempDir(), "mount"),
				aPod("reader", "pod-uid-1"))); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}

		client := fake.NewClientset(aVolume("example-store"), aVolume("another-store"))
		sweeper := newSweeping(answering.node, client, time.Hour, quietLogger())
		go sweeper.follow(t.Context())

		waitForCopies(t, answering, "example-store", "third-copy")
	})
}

func TestTheSweepLeavesTheCopyOfAVolumeOfAnotherDriver(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("some-cache"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	named := handlesOf([]any{
		&corev1.Pod{},
		&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "host-path"}},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "some-cache"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver: "git.liken.sh", VolumeHandle: "some-cache",
					},
				},
			},
		},
	})
	if len(named) != 0 {
		t.Fatalf("handlesOf named %v, want none of them", named)
	}
}

func TestTheHandlesTheClusterNamesAreThisDriversOwn(t *testing.T) {
	named := handlesOf([]any{aVolume("example-store"), aVolume("some-cache")})
	if len(named) != 2 || !named["example-store"] || !named["some-cache"] {
		t.Errorf("handlesOf named %v, want example-store and some-cache", named)
	}
}

func TestADeletedVolumeLosesItsCopyBeforeTheNextTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		for _, handle := range []string{"example-store", "some-cache", "third-copy"} {
			if err := answering.store.makeCopy(handle); err != nil {
				t.Fatalf("makeCopy: %v", err)
			}
		}
		client := fake.NewClientset(aVolume("example-store"), aVolume("some-cache"))
		sweeper := newSweeping(answering.node, client, time.Hour, quietLogger())
		go sweeper.follow(t.Context())
		// The third copy goes on the first pass, which is the proof that the
		// informer has synced and that the pass below follows the delete.
		waitForCopies(t, answering, "example-store", "some-cache")

		if err := client.CoreV1().PersistentVolumes().Delete(t.Context(),
			"some-cache", metav1.DeleteOptions{}); err != nil {
			t.Fatalf("deleting the volume: %v", err)
		}
		waitForCopies(t, answering, "example-store")
	})
}

func TestTheSweepFindsAnOrphanOnItsTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		if err := answering.store.makeCopy("example-store"); err != nil {
			t.Fatalf("makeCopy: %v", err)
		}
		sweeper := newSweeping(answering.node, fake.NewClientset(), 10*time.Millisecond, quietLogger())
		go sweeper.follow(t.Context())
		waitForCopies(t, answering)

		// Nothing is deleted from here on, so the tick is the only pass that
		// can find this copy.
		if err := answering.store.makeCopy("some-cache"); err != nil {
			t.Fatalf("makeCopy: %v", err)
		}
		waitForCopies(t, answering, "some-cache")
		time.Sleep(10 * time.Millisecond)
		waitForCopies(t, answering)
	})
}

func TestTheSweepStopsWithTheDriversRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		if err := answering.store.makeCopy("example-store"); err != nil {
			t.Fatalf("makeCopy: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		sweeper := newSweeping(answering.node, fake.NewClientset(), time.Hour, quietLogger())
		stopped := make(chan struct{})
		go func() {
			sweeper.follow(ctx)
			close(stopped)
		}()
		waitForCopies(t, answering)

		// A sweep that outlives its context blocks the bubble for good, and
		// synctest fails the test.
		cancel()
		<-stopped
	})
}

func TestADriverThatReachedNoClusterSweepsNothing(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	written := &bytes.Buffer{}
	sweeper := newSweeping(answering.node, nil, time.Hour,
		slog.New(slog.NewTextHandler(written, nil)))
	sweeper.follow(t.Context())

	if got := copiesIn(t, answering); len(got) != 1 {
		t.Errorf("the store holds %v, want the copy kept", got)
	}
	if !strings.Contains(written.String(), "no sweep") {
		t.Errorf("the log reads %q, want it to say why nothing was swept", written)
	}
}

func TestADriverWhoseVolumesNeverSyncSweepsNothing(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	written := &bytes.Buffer{}
	sweeper := newSweeping(answering.node, fake.NewClientset(), time.Hour,
		slog.New(slog.NewTextHandler(written, nil)))
	sweeper.follow(ctx)

	if got := copiesIn(t, answering); len(got) != 1 {
		t.Errorf("the store holds %v, want the copy kept", got)
	}
	if !strings.Contains(written.String(), "did not sync") {
		t.Errorf("the log reads %q, want it to say the volumes did not sync", written)
	}
}

func TestACopyTheDriverCannotRemoveIsLoggedAndTheSweepGoesOn(t *testing.T) {
	answering := testDriver(t)
	if err := answering.store.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(answering.store.copyPath("example-store"), "inner"),
		0o755); err != nil {
		t.Fatalf("making the inner directory: %v", err)
	}
	copies := filepath.Join(answering.store.root, copiesDirectory)
	if err := os.Chmod(copies, 0o500); err != nil {
		t.Fatalf("closing the copies directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(copies, 0o700) })

	written := &bytes.Buffer{}
	sweeper := newSweeping(answering.node, fake.NewClientset(), time.Hour,
		slog.New(slog.NewTextHandler(written, nil)))
	sweeper.sweep(t.Context(), map[string]bool{})

	if !strings.Contains(written.String(), "the copy stayed") {
		t.Errorf("the log reads %q, want it to say the copy stayed", written)
	}
}

func TestARestartedWatchCountsOnPerNodeCSIWatchRestartsTotal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering := testDriver(t)
		client := fake.NewClientset()
		// Every watch the API server answers closes at once, so the sweep
		// opens it again.
		client.PrependWatchReactor("persistentvolumes",
			func(k8stesting.Action) (bool, watch.Interface, error) {
				closed := watch.NewFake()
				closed.Stop()
				return true, closed, nil
			})
		ctx, stop := context.WithCancel(t.Context())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			newSweeping(answering.node, client, time.Hour, quietLogger()).follow(ctx)
		}()

		// The reflector waits out a backoff of less than a minute before it
		// opens a watch again.
		time.Sleep(time.Minute)
		stop()
		<-stopped

		restarts := answering.readings.watchRestarts.WithLabelValues(watchedKind)

		if got := testutil.ToFloat64(restarts); got == 0 {
			t.Errorf("per_node_csi_watch_restarts_total reads %v, want at least one restart", got)
		}
	})
}

func TestTheInformerStoresNoManagedFields(t *testing.T) {
	held := aVolume("example-store")
	held.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}

	stored, err := dropManagedFields(held)

	if err != nil {
		t.Fatalf("the transform failed: %v", err)
	}
	if fields := stored.(*corev1.PersistentVolume).ManagedFields; fields != nil {
		t.Errorf("the informer stores managedFields %v, want none", fields)
	}
}

// waitForCopies waits until every goroutine in the bubble is blocked,
// because the sweep runs on a goroutine of its own, and fails unless the
// store then holds exactly the wanted handles.
func waitForCopies(t *testing.T, answering *driver, want ...string) {
	t.Helper()
	synctest.Wait()
	if got := copiesIn(t, answering); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the store holds %v, want %v", got, want)
	}
}
