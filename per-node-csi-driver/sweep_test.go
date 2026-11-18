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

func copiesIn(t *testing.T, d *driver) []string {
	t.Helper()
	return d.store.handles()
}

func TestTheSweepRemovesACopyThatNoVolumeNamesAndNoPodMounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		for _, handle := range []string{"example-store", "some-cache", "third-copy"} {
			if err := d.store.makeCopy(handle); err != nil {
				t.Fatalf("making the copy: %v", err)
			}
		}
		// A pod mounts third-copy, so the sweep keeps it although no
		// PersistentVolume names it.
		if _, err := d.NodePublishVolume(t.Context(),
			publishing("third-copy", filepath.Join(t.TempDir(), "mount"),
				aPod("reader", "pod-uid-1"))); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}

		client := fake.NewClientset(aVolume("example-store"), aVolume("another-store"))
		sweeper := newSweeper(d.node, client, time.Hour, quietLogger())
		go sweeper.run(t.Context())

		waitForCopies(t, d, "example-store", "third-copy")
	})
}

func TestTheSweepLeavesTheCopyOfAVolumeOfAnotherDriver(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("some-cache"); err != nil {
		t.Fatalf("making the copy: %v", err)
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
		d := testDriver(t)
		for _, handle := range []string{"example-store", "some-cache", "third-copy"} {
			if err := d.store.makeCopy(handle); err != nil {
				t.Fatalf("making the copy: %v", err)
			}
		}
		client := fake.NewClientset(aVolume("example-store"), aVolume("some-cache"))
		sweeper := newSweeper(d.node, client, time.Hour, quietLogger())
		go sweeper.run(t.Context())
		// The first sweep deletes third-copy, which shows that the
		// informer has synced. The ticker runs once an hour, so only the
		// sweep that the delete event starts can remove some-cache.
		waitForCopies(t, d, "example-store", "some-cache")

		if err := client.CoreV1().PersistentVolumes().Delete(t.Context(),
			"some-cache", metav1.DeleteOptions{}); err != nil {
			t.Fatalf("deleting the volume: %v", err)
		}
		waitForCopies(t, d, "example-store")
	})
}

func TestTheSweepFindsAnOrphanOnItsTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		if err := d.store.makeCopy("example-store"); err != nil {
			t.Fatalf("making the copy: %v", err)
		}
		sweeper := newSweeper(d.node, fake.NewClientset(), 10*time.Millisecond, quietLogger())
		go sweeper.run(t.Context())
		waitForCopies(t, d)

		// No PersistentVolume is deleted from here on, so only the ticker
		// can start the sweep that removes this copy.
		if err := d.store.makeCopy("some-cache"); err != nil {
			t.Fatalf("making the copy: %v", err)
		}
		waitForCopies(t, d, "some-cache")
		time.Sleep(10 * time.Millisecond)
		waitForCopies(t, d)
	})
}

func TestTheSweepStopsWithTheDriversRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		if err := d.store.makeCopy("example-store"); err != nil {
			t.Fatalf("making the copy: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		sweeper := newSweeper(d.node, fake.NewClientset(), time.Hour, quietLogger())
		stopped := make(chan struct{})
		go func() {
			sweeper.run(ctx)
			close(stopped)
		}()
		waitForCopies(t, d)

		// If run outlived its context, the synctest bubble would never
		// finish and the test would fail.
		cancel()
		<-stopped
	})
}

func TestADriverThatReachedNoClusterSweepsNothing(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("example-store"); err != nil {
		t.Fatalf("making the copy: %v", err)
	}
	written := &bytes.Buffer{}
	sweeper := newSweeper(d.node, nil, time.Hour,
		slog.New(slog.NewTextHandler(written, nil)))
	sweeper.run(t.Context())

	if got := copiesIn(t, d); len(got) != 1 {
		t.Errorf("the store has copies %v, want the copy kept", got)
	}
	if !strings.Contains(written.String(), "Not sweeping the copies") {
		t.Errorf("the log reads %q, want it to say why nothing was swept", written)
	}
}

func TestADriverWhoseVolumesNeverSyncSweepsNothing(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("example-store"); err != nil {
		t.Fatalf("making the copy: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	written := &bytes.Buffer{}
	sweeper := newSweeper(d.node, fake.NewClientset(), time.Hour,
		slog.New(slog.NewTextHandler(written, nil)))
	sweeper.run(ctx)

	if got := copiesIn(t, d); len(got) != 1 {
		t.Errorf("the store has copies %v, want the copy kept", got)
	}
	if !strings.Contains(written.String(), "did not sync before the driver stopped") {
		t.Errorf("the log reads %q, want it to say the volumes did not sync", written)
	}
}

func TestACopyTheDriverCannotRemoveIsLoggedAndTheSweepGoesOn(t *testing.T) {
	d := testDriver(t)
	if err := d.store.makeCopy("example-store"); err != nil {
		t.Fatalf("making the copy: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(d.store.copyPath("example-store"), "inner"),
		0o755); err != nil {
		t.Fatalf("making the inner directory: %v", err)
	}
	copies := filepath.Join(d.store.root, copiesDirectory)
	if err := os.Chmod(copies, 0o500); err != nil {
		t.Fatalf("closing the copies directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(copies, 0o700) })

	written := &bytes.Buffer{}
	sweeper := newSweeper(d.node, fake.NewClientset(), time.Hour,
		slog.New(slog.NewTextHandler(written, nil)))
	sweeper.sweep(t.Context(), map[string]bool{})

	if !strings.Contains(written.String(), "Could not delete the copy") {
		t.Errorf("the log reads %q, want it to say the sweep did not delete the copy", written)
	}
}

func TestARestartedWatchCountsOnPerNodeCSIWatchRestartsTotal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := testDriver(t)
		client := fake.NewClientset()
		// Every watch closes at once, so the reflector opens it again.
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
			newSweeper(d.node, client, time.Hour, quietLogger()).run(ctx)
		}()

		// The reflector waits for a backoff of less than a minute before
		// it opens a watch again.
		time.Sleep(time.Minute)
		stop()
		<-stopped

		restarts := d.metrics.watchRestarts.WithLabelValues(watchedKind)

		if got := testutil.ToFloat64(restarts); got == 0 {
			t.Errorf("per_node_csi_watch_restarts_total reads %v, want at least one restart", got)
		}
	})
}

func TestTheInformerStoresNoManagedFields(t *testing.T) {
	volume := aVolume("example-store")
	volume.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}

	stored, err := dropManagedFields(volume)

	if err != nil {
		t.Fatalf("dropManagedFields: %v", err)
	}
	if fields := stored.(*corev1.PersistentVolume).ManagedFields; fields != nil {
		t.Errorf("the informer stores managedFields %v, want none", fields)
	}
}

// waitForCopies waits until every goroutine in the bubble is blocked,
// because the sweep runs on a goroutine of its own, and fails unless the
// store then has exactly the wanted handles.
func waitForCopies(t *testing.T, d *driver, want ...string) {
	t.Helper()
	synctest.Wait()
	if got := copiesIn(t, d); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the store has copies %v, want %v", got, want)
	}
}

func TestAWakeWhileASweepIsPendingIsDropped(t *testing.T) {
	pending := make(chan struct{}, 1)
	wake(pending)
	wake(pending)
	if len(pending) != 1 {
		t.Errorf("the channel has %d pending wakes, want 1", len(pending))
	}
}
