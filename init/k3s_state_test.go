package main

// Tests for k3s's on-disk state: the seed files clusterState receives
// when it mounts, and the control-plane datastore a demotion leaves
// behind. The drop-in rendering and role derivations are k3s_test.go's
// side of the seam; the mounts themselves are QEMU territory.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func leaderDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(filepath.Join(db, "etcd", "member"), 0o755); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAProvenFollowerPurgesLeaderLeftovers(t *testing.T) {
	db := leaderDB(t)
	purgeLeaderLeftovers(machine.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Error("a proven follower keeps no control-plane datastore")
	}
}

func TestAStagedFollowerBootKeepsTheDatastore(t *testing.T) {
	// The trial boot of a document that demotes this machine must not
	// destroy anything: if the document fails to prove, the fallback
	// boots the leader role again and needs its datastore intact.
	db := leaderDB(t)
	purgeLeaderLeftovers(machine.RoleFollower, machine.ManifestSourceStaged, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("an unproven demotion must leave the datastore alone")
	}
}

func TestALeaderKeepsItsDatastore(t *testing.T) {
	db := leaderDB(t)
	purgeLeaderLeftovers(machine.RoleLeader, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("a leader's datastore is the cluster; hands off")
	}
}

// fakeSeedSource stands in for the image's /var/lib/rancher tree.
func fakeSeedSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range []string{"k3s/server/tls", "k3s/server/manifests", "k3s/agent/images"} {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel, "seeded"), []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := seedSourceDir
	seedSourceDir = dir
	t.Cleanup(func() { seedSourceDir = old })
	return dir
}

func TestSeedClusterStateCopiesTheSeeds(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	if err := seedClusterState(root); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"k3s/server/tls", "k3s/server/manifests", "k3s/agent/images"} {
		if _, err := os.Stat(filepath.Join(root, rel, "seeded")); err != nil {
			t.Errorf("%s should be seeded: %v", rel, err)
		}
	}
}

func TestSeedClusterStateKeepsIdentityAndRefreshesManifests(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	// The disk already carries an identity and an old manifest tree.
	for _, rel := range []string{"k3s/server/tls", "k3s/server/manifests"} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel, "existing"), []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := seedClusterState(root); err != nil {
		t.Fatal(err)
	}
	// TLS is identity: the disk's copy wins, and the seed never lands.
	if _, err := os.Stat(filepath.Join(root, "k3s/server/tls", "existing")); err != nil {
		t.Error("a disk that has an identity keeps it")
	}
	if _, err := os.Stat(filepath.Join(root, "k3s/server/tls", "seeded")); err == nil {
		t.Error("the seed must not overwrite existing TLS material")
	}
	// Manifests are the running image's: refreshed wholesale.
	if _, err := os.Stat(filepath.Join(root, "k3s/server/manifests", "existing")); err == nil {
		t.Error("old manifests are replaced by the image's")
	}
	if _, err := os.Stat(filepath.Join(root, "k3s/server/manifests", "seeded")); err != nil {
		t.Error("the image's manifests land on every boot")
	}
}

func TestSeedClusterStateWithoutSeedsIsANoOp(t *testing.T) {
	old := seedSourceDir
	seedSourceDir = filepath.Join(t.TempDir(), "nothing")
	t.Cleanup(func() { seedSourceDir = old })
	if err := seedClusterState(t.TempDir()); err != nil {
		t.Errorf("an image without k3s has no seed files, and that's fine: %v", err)
	}
}

func TestPurgeLeaderLeftoversReportsAFailedRemoval(t *testing.T) {
	parent := t.TempDir()
	db := filepath.Join(parent, "db")
	if err := os.MkdirAll(filepath.Join(db, "etcd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	purgeLeaderLeftovers(machine.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("a failed purge leaves the datastore; the error is reported, not hidden")
	}
}

func TestPurgeLeaderLeftoversWithNoDatastoreDoesNothing(t *testing.T) {
	// A follower that was never a leader has nothing to purge; the
	// missing directory is the ordinary case, not an error.
	purgeLeaderLeftovers(machine.RoleFollower, machine.ManifestSourceProven,
		filepath.Join(t.TempDir(), "absent"))
}

func TestPersistNodePasswordSkipsAMemoryBackedMachine(t *testing.T) {
	// Nothing about a memory-backed machine survives a reboot, so the
	// tmpfs default is left alone and no symlink is attempted.
	persistNodePassword(machine.AllRolesInMemory())
}

func TestSeedClusterStateReportsAnUnreadableSeed(t *testing.T) {
	dir := fakeSeedSource(t)
	sealed := filepath.Join(dir, "k3s/server/manifests/seeded")
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o644) })

	if err := seedClusterState(t.TempDir()); err == nil {
		t.Error("a seed that can't be copied is an error the mount must hear about")
	}
}
