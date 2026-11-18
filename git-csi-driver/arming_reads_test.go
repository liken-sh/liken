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

// These tests run arming.armFromClaims with no claim watch, and send it copies
// of the claim in the order that the watch would.

// claimWithClass returns the claim config with the class as its
// VolumeAttributesClass.
func claimWithClass(class string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "home", Name: "config"},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeAttributesClassName: &class},
	}
}

// startReads runs arming.armFromClaims until the context ends. It returns the
// channel that the test sends copies of the claim on, and a channel that
// closes when armFromClaims returns.
func startReads(
	ctx context.Context, n *node, vol *volume,
) (chan *corev1.PersistentVolumeClaim, <-chan struct{}) {
	latest := make(chan *corev1.PersistentVolumeClaim, 1)
	over := make(chan struct{})
	go func() {
		defer close(over)
		n.arming.armFromClaims(ctx, vol, claimReference{namespace: "home", name: "config"}, latest)
	}()
	return latest, over
}

// waitForLog waits until the bubble is blocked, and fails unless the
// log contains the text.
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
		n, _ := testNode(t, logs)
		createClass(t, n, "config-eager", nil)
		vol := volumeNamed("config")
		latest, _ := startReads(t.Context(), n, vol)

		// The first copy's class does not exist, so only the newer copy
		// can arm the volume.
		latest <- claimWithClass("missing")
		waitForLog(t, logs, `msg="The claim and its VolumeAttributesClass were not read"`)
		latest <- claimWithClass("config-eager")

		// The retry reads the newer copy.
		time.Sleep(n.arming.retry)
		waitForArmed(t, vol, true)
	})
}

func TestTheReadsEndWithTheDriverWhileAReadFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, _ := testNode(t, logs)
		n.arming.retry = 30 * time.Second
		ctx, stop := context.WithCancel(t.Context())
		latest, over := startReads(ctx, n, volumeNamed("config"))

		latest <- claimWithClass("missing")
		waitForLog(t, logs, `msg="The claim and its VolumeAttributesClass were not read"`)
		stop()
		<-over
	})
}
