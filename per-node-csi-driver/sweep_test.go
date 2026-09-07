package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
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
}

func TestTheSweepFindsAnOrphanOnItsTick(t *testing.T) {
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
	waitForCopies(t, answering)
}

func TestTheSweepStopsWithTheDriversRun(t *testing.T) {
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

	cancel()
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("the sweep did not stop with the run")
	}
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

func TestAnInformerThatHasStoppedWatchesNoDeletes(t *testing.T) {
	answering := testDriver(t)
	written := &bytes.Buffer{}
	sweeper := newSweeping(answering.node, fake.NewClientset(), time.Hour,
		slog.New(slog.NewTextHandler(written, nil)))
	sweeper.watchDeletes(t.Context(), stoppedInformer(t, sweeper.client), make(chan struct{}, 1))

	if !strings.Contains(written.String(), "the deletes are not watched") {
		t.Errorf("the log reads %q, want it to say the deletes are not watched", written)
	}
}

// stoppedInformer returns an informer whose run is over. A stopped
// informer refuses a new handler, and nothing else makes
// AddEventHandler fail.
func stoppedInformer(t *testing.T, client kubernetes.Interface) cache.SharedIndexInformer {
	t.Helper()
	informer := informers.NewSharedInformerFactory(client, time.Hour).
		Core().V1().PersistentVolumes().Informer()
	stop := make(chan struct{})
	go informer.Run(stop)
	cache.WaitForCacheSync(stop, informer.HasSynced)
	close(stop)
	deadline := time.Now().Add(10 * time.Second)
	for !informer.IsStopped() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return informer
}

// waitForCopies waits until the store holds exactly the wanted handles,
// because the sweep runs on a goroutine of its own.
func waitForCopies(t *testing.T, answering *driver, want ...string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Join(copiesIn(t, answering), " ") == strings.Join(want, " ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the store holds %v, want %v", copiesIn(t, answering), want)
}
