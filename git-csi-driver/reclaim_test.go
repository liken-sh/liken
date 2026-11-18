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

// unstagedVolume stages a writeable volume and unstages it, which
// leaves the work tree as it is when its claim is deleted.
func unstagedVolume(t *testing.T, n *node, id, url string) *volume {
	t.Helper()
	vol, request := stagedVolume(t, n, id, url)
	unstage(t, n, request)
	return vol
}

// unstage sends NodeUnstageVolume, as the kubelet does after the last
// pod on the node that uses the volume stops.
func unstage(t *testing.T, n *node, request *csi.NodeStageVolumeRequest) {
	t.Helper()
	if _, err := n.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: request.GetVolumeId(), StagingTargetPath: request.GetStagingTargetPath(),
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
}

func reclaimedVolume(t *testing.T, n *node, name, handle string,
	policy corev1.PersistentVolumeReclaimPolicy,
) {
	t.Helper()
	pv := csiVolume(handle, driverName)
	pv.Name = name
	pv.Spec.PersistentVolumeReclaimPolicy = policy
	if _, err := cluster(t, n).CoreV1().PersistentVolumes().
		Create(t.Context(), pv, metav1.CreateOptions{}); err != nil {
		t.Fatalf("writing the PersistentVolume: %v", err)
	}
}

// changePolicy updates the reclaim policy of a PersistentVolume, as a
// person would with kubectl edit.
func changePolicy(t *testing.T, n *node, name string,
	policy corev1.PersistentVolumeReclaimPolicy,
) {
	t.Helper()
	volumes := cluster(t, n).CoreV1().PersistentVolumes()
	pv, err := volumes.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the PersistentVolume: %v", err)
	}
	pv.Spec.PersistentVolumeReclaimPolicy = policy
	if _, err := volumes.Update(t.Context(), pv, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("changing the reclaim policy: %v", err)
	}
}

// deleteVolume deletes the PersistentVolume, which is what
// csi-provisioner does after DeleteVolume returns.
func deleteVolume(t *testing.T, n *node, name string) {
	t.Helper()
	if err := cluster(t, n).CoreV1().PersistentVolumes().
		Delete(t.Context(), name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting the PersistentVolume: %v", err)
	}
}

// watchCluster starts the node's watch on PersistentVolumes and returns
// after the first list and the reclaim that follows it. The sleep
// advances the synctest bubble's fake clock, which the watch needs to
// make progress.
func watchCluster(t *testing.T, n *node, ctx context.Context) {
	t.Helper()
	go n.demands.run(ctx)
	for !n.demands.listed.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	synctest.Wait()
}

// treeKept checks that the volume's directory and the URL's bare
// repository both exist when want is true, and are both gone when want
// is false.
func treeKept(t *testing.T, n *node, id, url string, want bool) {
	t.Helper()
	_, err := os.Stat(n.store.volumeDir(id))
	if kept := err == nil; kept != want {
		t.Errorf("the work tree is kept: %v, want %v", kept, want)
	}
	_, err = os.Stat(n.store.repository(url).dir)
	if kept := err == nil; kept != want {
		t.Errorf("the bare repository is kept: %v, want %v", kept, want)
	}
}

func TestDeletingAPersistentVolumeKeepsOrRemovesTheWorkTreeByReclaimPolicy(t *testing.T) {
	for _, c := range []struct {
		name   string
		policy corev1.PersistentVolumeReclaimPolicy
		// changed is the policy a person sets after the stage, if not
		// empty.
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
				n, _ := testNode(t, io.Discard)
				url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
				reclaimedVolume(t, n, "config", "config", c.policy)
				watchCluster(t, n, t.Context())
				unstagedVolume(t, n, "config", url)
				if c.changed != "" {
					changePolicy(t, n, "config", c.changed)
				}

				deleteVolume(t, n, "config")
				synctest.Wait()

				treeKept(t, n, "config", url, c.kept)
			})
		})
	}
}

func TestAStagedWorkTreeIsRemovedAtTheUnstage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, _ := testNode(t, logs)
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		url := fileURL(remote)
		reclaimedVolume(t, n, "config", "config", corev1.PersistentVolumeReclaimDelete)
		watchCluster(t, n, t.Context())
		vol, request := stagedVolume(t, n, "config", url)
		driverCommit(t, n, vol, map[string]string{"b.txt": "two"})

		deleteVolume(t, n, "config")
		synctest.Wait()
		treeKept(t, n, "config", url, true)

		unstage(t, n, request)
		treeKept(t, n, "config", url, false)
		if got := strings.TrimSpace(git(t, remote, "show", "main:b.txt")); got != "two" {
			t.Errorf("b.txt on the remote is %q, want the pod's last write, two", got)
		}
		if !strings.Contains(logs.String(), "Removed the work tree") {
			t.Errorf("the log is %q, want it to report the removal of the work tree", logs)
		}
	})
}

func TestAWorkTreeStaysWhileAnotherPersistentVolumeHasItsHandle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
		reclaimedVolume(t, n, "config", "config", corev1.PersistentVolumeReclaimDelete)
		reclaimedVolume(t, n, "config-again", "config", corev1.PersistentVolumeReclaimDelete)
		watchCluster(t, n, t.Context())
		unstagedVolume(t, n, "config", url)

		deleteVolume(t, n, "config")
		synctest.Wait()

		treeKept(t, n, "config", url, true)
	})
}

func TestANodePluginThatMissedTheDeleteRemovesTheTreeAtStartup(t *testing.T) {
	for _, c := range []struct {
		name   string
		policy corev1.PersistentVolumeReclaimPolicy
		// forgotten deletes the policy file.
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
				n, _ := testNode(t, io.Discard)
				url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
				reclaimedVolume(t, n, "config", "config", c.policy)
				first, stop := context.WithCancel(t.Context())
				watchCluster(t, n, first)
				unstagedVolume(t, n, "config", url)
				if c.forgotten {
					if err := os.Remove(n.store.policyFile("config")); err != nil {
						t.Fatalf("removing the recorded policy: %v", err)
					}
				}
				stop()
				synctest.Wait()

				deleteVolume(t, n, "config")
				watchCluster(t, n, t.Context())

				treeKept(t, n, "config", url, c.kept)
			})
		})
	}
}

func TestOnlyASinglePathElementIsADirectoryName(t *testing.T) {
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
			if got := isDirectoryName(c.handle); got != c.one {
				t.Errorf("isDirectoryName(%q) = %v, want %v", c.handle, got, c.one)
			}
		})
	}
}

func TestAPolicyOfAHandleOutsideTheStoreIsNotRecorded(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	pv := csiVolume("..", driverName)
	pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete

	n.recordReclaimPolicy(t.Context(), pv)

	if _, err := os.Stat(filepath.Join(n.store.root, policyFileName)); err == nil {
		t.Error("the policy was written outside the volume directories")
	}
}

func TestTheNodeReportsWhatItCannotRecordOrRemove(t *testing.T) {
	for _, c := range []struct {
		name string
		// block makes the store refuse the write that this case needs.
		block       func(t *testing.T, n *node)
		wantMessage string
	}{
		{
			name: "a policy it cannot record",
			block: func(t *testing.T, n *node) {
				readOnlyDir(t, n.store.policyFile("config"))
				changePolicy(t, n, "config", corev1.PersistentVolumeReclaimRetain)
			},
			wantMessage: "The reclaim policy file was not written",
		},
		{
			name: "a work tree it cannot remove",
			block: func(t *testing.T, n *node) {
				readOnlyDir(t, filepath.Dir(n.store.volumeDir("config")))
				deleteVolume(t, n, "config")
			},
			wantMessage: "The work tree was not removed",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := &logbook{}
				n, _ := testNode(t, logs)
				url := fileURL(bareRemote(t, map[string]string{"a.txt": "one"}))
				reclaimedVolume(t, n, "config", "config", corev1.PersistentVolumeReclaimDelete)
				watchCluster(t, n, t.Context())
				unstagedVolume(t, n, "config", url)

				c.block(t, n)
				synctest.Wait()

				if !strings.Contains(logs.String(), c.wantMessage) {
					t.Errorf("the log is %q, want it to contain %q", logs, c.wantMessage)
				}
			})
		})
	}
}
