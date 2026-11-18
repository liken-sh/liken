package main

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// watchHolding starts the node's watch on PersistentVolumes and waits
// until every goroutine in the bubble is blocked, so the informer has
// listed every PersistentVolume created before the call.
func watchHolding(t *testing.T, n *node) {
	t.Helper()
	watchDemands(t, n)
	synctest.Wait()
}

// volumeLists counts the list requests for PersistentVolumes that the
// fake API server received since the last ClearActions.
func volumeLists(t *testing.T, n *node) int {
	t.Helper()
	count := 0
	for _, action := range cluster(t, n).Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "persistentvolumes" {
			count++
		}
	}
	return count
}

func TestTheClaimComesFromTheWatchWhenItHoldsTheVolume(t *testing.T) {
	for _, c := range []struct {
		name        string
		setup       func(t *testing.T, n *node)
		watch       func(t *testing.T, n *node)
		found       claimReference
		wantMessage string
		lists       int
	}{
		{
			name:        "the watch store has the bound PersistentVolume",
			setup:       func(t *testing.T, n *node) { boundVolume(t, n, "config", "") },
			watch:       watchHolding,
			found:       claimReference{namespace: "home", name: "config"},
			wantMessage: "<nil>",
			lists:       0,
		},
		{
			name:        "no watch runs yet",
			setup:       func(t *testing.T, n *node) { boundVolume(t, n, "config", "") },
			watch:       func(*testing.T, *node) {},
			found:       claimReference{namespace: "home", name: "config"},
			wantMessage: "<nil>",
			lists:       1,
		},
		{
			name:        "the watch store has no PersistentVolume of the handle",
			setup:       func(*testing.T, *node) {},
			watch:       watchHolding,
			wantMessage: "has the volume handle",
			lists:       1,
		},
		{
			name: "the watch store has the handle only on another driver's PersistentVolume",
			setup: func(t *testing.T, n *node) {
				other := csiVolume("config", "other.example.com")
				other.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "home", Name: "config"}
				if _, err := cluster(t, n).CoreV1().PersistentVolumes().Create(t.Context(),
					other, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			watch:       watchHolding,
			wantMessage: "has the volume handle",
			lists:       1,
		},
		{
			name: "the watch store has two PersistentVolumes of the handle",
			setup: func(t *testing.T, n *node) {
				boundVolume(t, n, "config", "")
				second := csiVolume("config", driverName)
				second.Name = "config-copy"
				if _, err := cluster(t, n).CoreV1().PersistentVolumes().Create(t.Context(),
					second, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the second PersistentVolume: %v", err)
				}
			},
			watch:       watchHolding,
			found:       claimReference{namespace: "home", name: "config"},
			wantMessage: "<nil>",
			lists:       1,
		},
		{
			name: "the watch store has the PersistentVolume released from its claim",
			setup: func(t *testing.T, n *node) {
				released := csiVolume("config", driverName)
				released.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "home", Name: "config"}
				released.Status.Phase = corev1.VolumeReleased
				if _, err := cluster(t, n).CoreV1().PersistentVolumes().Create(t.Context(),
					released, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			watch:       watchHolding,
			found:       claimReference{namespace: "home", name: "config"},
			wantMessage: "<nil>",
			lists:       1,
		},
		{
			name: "the watch store has the PersistentVolume bound to no claim",
			setup: func(t *testing.T, n *node) {
				if _, err := cluster(t, n).CoreV1().PersistentVolumes().Create(t.Context(),
					csiVolume("config", driverName), metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			watch:       watchHolding,
			wantMessage: "bound to no claim",
			lists:       1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, _ := testNode(t, io.Discard)
				c.setup(t, n)
				c.watch(t, n)
				cluster(t, n).ClearActions()

				found, err := n.arming.claimOf(t.Context(), "config")

				if found != c.found {
					t.Errorf("claimOf returned %+v, want %+v", found, c.found)
				}
				if got := fmt.Sprint(err); !strings.Contains(got, c.wantMessage) {
					t.Errorf("claimOf returned the error %q, want %q in it", got, c.wantMessage)
				}
				if lists := volumeLists(t, n); lists != c.lists {
					t.Errorf("claimOf listed PersistentVolumes %d times of PersistentVolumes, want %d", lists, c.lists)
				}
			})
		})
	}
}
