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
// until the bubble is blocked. The informer has then listed every
// PersistentVolume written before the call, and its store holds them.
func watchHolding(t *testing.T, answering *node) {
	t.Helper()
	watchDemands(t, answering)
	synctest.Wait()
}

// volumeLists counts the lists of PersistentVolumes the fake API server
// answered since the last ClearActions.
func volumeLists(t *testing.T, answering *node) int {
	t.Helper()
	counted := 0
	for _, action := range cluster(t, answering).Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "persistentvolumes" {
			counted++
		}
	}
	return counted
}

func TestTheClaimComesFromTheWatchWhenItHoldsTheVolume(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node)
		watch func(t *testing.T, answering *node)
		found claimReference
		says  string
		lists int
	}{
		{
			name:  "the watch holds the bound PersistentVolume",
			stand: func(t *testing.T, answering *node) { boundVolume(t, answering, "config", "") },
			watch: watchHolding,
			found: claimReference{namespace: "home", name: "config"},
			says:  "<nil>",
			lists: 0,
		},
		{
			name:  "no watch runs yet",
			stand: func(t *testing.T, answering *node) { boundVolume(t, answering, "config", "") },
			watch: func(*testing.T, *node) {},
			found: claimReference{namespace: "home", name: "config"},
			says:  "<nil>",
			lists: 1,
		},
		{
			name:  "the watch holds no PersistentVolume of the handle",
			stand: func(*testing.T, *node) {},
			watch: watchHolding,
			says:  "carries the handle",
			lists: 1,
		},
		{
			name: "the watch holds the handle only on another driver's PersistentVolume",
			stand: func(t *testing.T, answering *node) {
				other := csiVolume("config", "other.example.com")
				other.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "home", Name: "config"}
				if _, err := cluster(t, answering).CoreV1().PersistentVolumes().Create(t.Context(),
					other, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			watch: watchHolding,
			says:  "carries the handle",
			lists: 1,
		},
		{
			name: "the watch holds two PersistentVolumes of the handle",
			stand: func(t *testing.T, answering *node) {
				boundVolume(t, answering, "config", "")
				second := csiVolume("config", driverName)
				second.Name = "config-copy"
				if _, err := cluster(t, answering).CoreV1().PersistentVolumes().Create(t.Context(),
					second, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the second PersistentVolume: %v", err)
				}
			},
			watch: watchHolding,
			found: claimReference{namespace: "home", name: "config"},
			says:  "<nil>",
			lists: 1,
		},
		{
			name: "the watch holds the PersistentVolume released from its claim",
			stand: func(t *testing.T, answering *node) {
				released := csiVolume("config", driverName)
				released.Spec.ClaimRef = &corev1.ObjectReference{Namespace: "home", Name: "config"}
				released.Status.Phase = corev1.VolumeReleased
				if _, err := cluster(t, answering).CoreV1().PersistentVolumes().Create(t.Context(),
					released, metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			watch: watchHolding,
			found: claimReference{namespace: "home", name: "config"},
			says:  "<nil>",
			lists: 1,
		},
		{
			name: "the watch holds the PersistentVolume bound to no claim",
			stand: func(t *testing.T, answering *node) {
				if _, err := cluster(t, answering).CoreV1().PersistentVolumes().Create(t.Context(),
					csiVolume("config", driverName), metav1.CreateOptions{}); err != nil {
					t.Fatalf("writing the PersistentVolume: %v", err)
				}
			},
			watch: watchHolding,
			says:  "bound to no claim",
			lists: 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				answering, _ := testNode(t, io.Discard)
				c.stand(t, answering)
				c.watch(t, answering)
				cluster(t, answering).ClearActions()

				found, err := answering.arms.claimOf(t.Context(), "config")

				if found != c.found {
					t.Errorf("claimOf answered %+v, want %+v", found, c.found)
				}
				if said := fmt.Sprint(err); !strings.Contains(said, c.says) {
					t.Errorf("claimOf said %q, want %q in it", said, c.says)
				}
				if lists := volumeLists(t, answering); lists != c.lists {
					t.Errorf("claimOf sent %d lists of PersistentVolumes, want %d", lists, c.lists)
				}
			})
		})
	}
}
