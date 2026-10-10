package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// unstagedVolume stages a writeable volume and unstages it, which is the
// state a work tree is in when its claim is deleted.
func unstagedVolume(t *testing.T, answering *node, id, url string) *volume {
	t.Helper()
	held, request := stagedVolume(t, answering, id, url)
	unstage(t, answering, request)
	return held
}

// unstage is the call the kubelet makes once the last pod of the volume
// on this node has stopped.
func unstage(t *testing.T, answering *node, request *csi.NodeStageVolumeRequest) {
	t.Helper()
	if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: request.GetVolumeId(), StagingTargetPath: request.GetStagingTargetPath(),
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
}

// reclaimedVolume writes a PersistentVolume of the driver, named for
// its handle, with the reclaim policy.
func reclaimedVolume(t *testing.T, answering *node, name, handle string,
	policy corev1.PersistentVolumeReclaimPolicy,
) {
	t.Helper()
	held := csiVolume(handle, driverName)
	held.Name = name
	held.Spec.PersistentVolumeReclaimPolicy = policy
	if _, err := cluster(t, answering).CoreV1().PersistentVolumes().
		Create(t.Context(), held, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
}

// changePolicy is a person who edits the reclaim policy of a
// PersistentVolume that exists.
func changePolicy(t *testing.T, answering *node, name string,
	policy corev1.PersistentVolumeReclaimPolicy,
) {
	t.Helper()
	volumes := cluster(t, answering).CoreV1().PersistentVolumes()
	held, err := volumes.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the PersistentVolume: %v", err)
	}
	held.Spec.PersistentVolumeReclaimPolicy = policy
	if _, err := volumes.Update(t.Context(), held, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("changing the reclaim policy: %v", err)
	}
}

// deleteVolume is the delete that csi-provisioner makes after
// DeleteVolume answers, or that a person makes.
func deleteVolume(t *testing.T, answering *node, name string) {
	t.Helper()
	if err := cluster(t, answering).CoreV1().PersistentVolumes().
		Delete(t.Context(), name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting the PersistentVolume: %v", err)
	}
}

// watchCluster starts the node's watch on PersistentVolumes and
// returns once its first read of the cluster is in the store and the
// reclaim that follows that read has finished. The watch checks for the
// read on client-go's 100-millisecond poll, so the sleep moves the
// bubble's clock to it.
func watchCluster(t *testing.T, answering *node, ctx context.Context) {
	t.Helper()
	go answering.demands.follow(ctx)
	for !answering.demands.listed.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	synctest.Wait()
}

// treeKept fails unless the store still holds the volume's directory
// and the bare repository of the URL, or fails unless it holds neither.
func treeKept(t *testing.T, answering *node, id, url string, want bool) {
	t.Helper()
	_, err := os.Stat(answering.store.volumeDir(id))
	if kept := err == nil; kept != want {
		t.Errorf("the work tree is kept: %v, want %v", kept, want)
	}
	_, err = os.Stat(answering.store.repository(url).dir)
	if kept := err == nil; kept != want {
		t.Errorf("the bare repository is kept: %v, want %v", kept, want)
	}
}

func TestADeletedPersistentVolumeTakesItsWorkTreeByItsReclaimPolicy(t *testing.T) {
	for _, c := range []struct {
		name   string
		policy corev1.PersistentVolumeReclaimPolicy
		// changed is the policy a person sets after the stage, and
		// empty for none.
		changed corev1.PersistentVolumeReclaimPolicy
		kept    bool
	}{
		{name: "Delete", policy: corev1.PersistentVolumeReclaimDelete, kept: false},
		{name: "Retain", policy: corev1.PersistentVolumeReclaimRetain, kept: true},
		{
			name:    "Retain changed to Delete",
			policy:  corev1.PersistentVolumeReclaimRetain,
			changed: corev1.PersistentVolumeReclaimDelete,
			kept:    false,
		},
		{
			name:    "Delete changed to Retain",
			policy:  corev1.PersistentVolumeReclaimDelete,
			changed: corev1.PersistentVolumeReclaimRetain,
			kept:    true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				answering, _ := testNode(t, io.Discard)
				url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
				reclaimedVolume(t, answering, "config", "config", c.policy)
				watchCluster(t, answering, t.Context())
				unstagedVolume(t, answering, "config", url)
				if c.changed != "" {
					changePolicy(t, answering, "config", c.changed)
				}

				deleteVolume(t, answering, "config")
				synctest.Wait()

				treeKept(t, answering, "config", url, c.kept)
			})
		})
	}
}

func TestAWorkTreeTheNodeStillHoldsGoesAtTheUnstage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		answering, _ := testNode(t, logs)
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		url := fileURL(remote)
		reclaimedVolume(t, answering, "config", "config", corev1.PersistentVolumeReclaimDelete)
		watchCluster(t, answering, t.Context())
		held, request := stagedVolume(t, answering, "config", url)
		driverCommit(t, answering, held, map[string]string{"b.txt": "two"})

		deleteVolume(t, answering, "config")
		synctest.Wait()
		treeKept(t, answering, "config", url, true)

		unstage(t, answering, request)
		treeKept(t, answering, "config", url, false)
		// The unstage pushes before the tree goes, so the last write
		// reaches the remote.
		if got := strings.TrimSpace(git(t, remote, "show", "main:b.txt")); got != "two" {
			t.Errorf("the remote holds b.txt = %q, want the pod's last write", got)
		}
		if !strings.Contains(logs.String(), "removed the work tree") {
			t.Errorf("the log is %q, want the removal in it", logs)
		}
	})
}

func TestAWorkTreeStaysWhileAnotherPersistentVolumeCarriesItsHandle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
		reclaimedVolume(t, answering, "config", "config", corev1.PersistentVolumeReclaimDelete)
		reclaimedVolume(t, answering, "config-again", "config", corev1.PersistentVolumeReclaimDelete)
		watchCluster(t, answering, t.Context())
		unstagedVolume(t, answering, "config", url)

		deleteVolume(t, answering, "config")
		synctest.Wait()

		treeKept(t, answering, "config", url, true)
	})
}

func TestANodePluginThatMissedTheDeleteReclaimsAtItsStart(t *testing.T) {
	for _, c := range []struct {
		name   string
		policy corev1.PersistentVolumeReclaimPolicy
		// forgotten removes the record of the policy, which is a work
		// tree that no stage recorded a policy for.
		forgotten bool
		kept      bool
	}{
		{name: "Delete", policy: corev1.PersistentVolumeReclaimDelete, kept: false},
		{name: "Retain", policy: corev1.PersistentVolumeReclaimRetain, kept: true},
		{
			name:      "no recorded policy",
			policy:    corev1.PersistentVolumeReclaimDelete,
			forgotten: true,
			kept:      true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				answering, _ := testNode(t, io.Discard)
				url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
				reclaimedVolume(t, answering, "config", "config", c.policy)
				first, stop := context.WithCancel(t.Context())
				watchCluster(t, answering, first)
				unstagedVolume(t, answering, "config", url)
				if c.forgotten {
					if err := os.Remove(answering.store.policyFile("config")); err != nil {
						t.Fatalf("removing the recorded policy: %v", err)
					}
				}
				stop()
				synctest.Wait()

				deleteVolume(t, answering, "config")
				watchCluster(t, answering, t.Context())

				treeKept(t, answering, "config", url, c.kept)
			})
		})
	}
}

func TestAHandleNamesOneDirectoryOfTheStore(t *testing.T) {
	for _, c := range []struct {
		handle string
		one    bool
	}{
		{handle: "config", one: true},
		{handle: "", one: false},
		{handle: ".", one: false},
		{handle: "..", one: false},
		{handle: "../volumes", one: false},
	} {
		t.Run(c.handle, func(t *testing.T) {
			if got := oneElement(c.handle); got != c.one {
				t.Errorf("oneElement(%q) = %v, want %v", c.handle, got, c.one)
			}
		})
	}
}

func TestAPolicyOfAHandleOutsideTheStoreIsNotRecorded(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := csiVolume("..", driverName)
	held.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete

	answering.notePolicy(t.Context(), held)

	if _, err := os.Stat(filepath.Join(answering.store.root, policyFileName)); err == nil {
		t.Error("the policy was written outside the volume directories")
	}
}

func TestTheNodeReportsWhatItCannotRecordOrRemove(t *testing.T) {
	for _, c := range []struct {
		name string
		// close makes the store refuse the write the case needs.
		close func(t *testing.T, answering *node)
		says  string
	}{
		{
			name: "a policy it cannot record",
			close: func(t *testing.T, answering *node) {
				readOnlyDir(t, answering.store.policyFile("config"))
				changePolicy(t, answering, "config", corev1.PersistentVolumeReclaimRetain)
			},
			says: "the reclaim policy was not recorded",
		},
		{
			name: "a work tree it cannot remove",
			close: func(t *testing.T, answering *node) {
				readOnlyDir(t, filepath.Dir(answering.store.volumeDir("config")))
				deleteVolume(t, answering, "config")
			},
			says: "the work tree stayed",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := &logbook{}
				answering, _ := testNode(t, logs)
				url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
				reclaimedVolume(t, answering, "config", "config", corev1.PersistentVolumeReclaimDelete)
				watchCluster(t, answering, t.Context())
				unstagedVolume(t, answering, "config", url)

				c.close(t, answering)
				synctest.Wait()

				if !strings.Contains(logs.String(), c.says) {
					t.Errorf("the log is %q, want %q in it", logs, c.says)
				}
			})
		})
	}
}
