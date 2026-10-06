package main

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These tests hand the arming reads copies of the claim directly, in
// the order the watch would offer them.

// claimNaming is the claim config naming the class.
func claimNaming(class string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &class},
	}
}

// startReads runs the reads until the context ends, and returns the
// slot the test offers copies on and the channel that closes when the
// reads end.
func startReads(
	ctx context.Context, answering *node, held *volume,
) (chan *corev1.PersistentVolumeClaim, <-chan struct{}) {
	latest := make(chan *corev1.PersistentVolumeClaim, 1)
	over := make(chan struct{})
	go func() {
		defer close(over)
		answering.arms.reads(ctx, held, claimReference{namespace: "home", name: "config"}, latest)
	}()
	return latest, over
}

// waitForLog waits until the bubble is blocked, and fails unless the
// log holds the text.
func waitForLog(t *testing.T, logs *logbook, text string) {
	t.Helper()
	synctest.Wait()
	if !strings.Contains(logs.String(), text) {
		t.Fatalf("the log is %q, want %q in it", logs, text)
	}
}

func TestANewerClaimReplacesOneThatFailedToRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		answering, _ := testNode(t, logs)
		armingClass(t, answering, "config-eager", nil)
		held := volumeNamed("config")
		latest, _ := startReads(t.Context(), answering, held)

		// The class the first copy names never arrives, so only the newer
		// copy can arm the volume.
		latest <- claimNaming("missing")
		waitForLog(t, logs, `msg="the claim was not read"`)
		latest <- claimNaming("config-eager")

		// The reads take the newer copy after the retry.
		time.Sleep(answering.arms.retry)
		waitForArmed(t, held, true)
	})
}

func TestTheReadsEndWithTheDriverWhileAReadFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		answering, _ := testNode(t, logs)
		answering.arms.retry = 30 * time.Second
		ctx, stop := context.WithCancel(t.Context())
		latest, over := startReads(ctx, answering, volumeNamed("config"))

		latest <- claimNaming("missing")
		waitForLog(t, logs, `msg="the claim was not read"`)
		stop()
		<-over
	})
}
