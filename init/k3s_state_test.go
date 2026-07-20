package main

// Tests for k3s's on-disk state: the seed files that clusterState
// receives when it mounts, and the control-plane datastore that a
// demotion leaves behind. The drop-in rendering and role derivations
// are k3s_test.go's side of the split. The mounts themselves run
// only under QEMU.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
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
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Error("a proven follower keeps no control-plane datastore")
	}
}

func TestAStagedFollowerBootKeepsTheDatastore(t *testing.T) {
	// The trial boot of a document that demotes this machine must not
	// destroy anything: if the document fails to prove, the fallback
	// boots the leader role again and needs its datastore intact.
	db := leaderDB(t)
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceStaged, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("an unproven demotion must leave the datastore alone")
	}
}

func TestALeaderKeepsItsDatastore(t *testing.T) {
	db := leaderDB(t)
	purgeLeaderLeftovers(api.RoleLeader, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("a leader's datastore is the cluster; hands off")
	}
}

// fakeSeedSource substitutes for the image's /var/lib/rancher tree.
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
	// Manifests belong to the running image: they refresh completely.
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
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("a failed purge leaves the datastore; the error is reported, not hidden")
	}
}

func TestPurgeLeaderLeftoversWithNoDatastoreDoesNothing(t *testing.T) {
	// A follower that was never a leader has nothing to purge. The
	// missing directory is the ordinary case, not an error.
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceProven,
		filepath.Join(t.TempDir(), "absent"))
}

func TestPersistNodePasswordSkipsAMemoryBackedMachine(t *testing.T) {
	// Nothing about a memory-backed machine survives a reboot, so this
	// code leaves the tmpfs default alone and attempts no symlink.
	persistNodePassword(machine.AllRolesInMemory())
}

func TestMintNodePasswordWritesOne(t *testing.T) {
	dir := t.TempDir()
	if err := mintNodePassword(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}\n$`).Match(raw) {
		t.Errorf("password %q is not 32 hex characters", raw)
	}
	info, err := os.Stat(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("password mode: got %v, want 0600", info.Mode().Perm())
	}
}

func TestMintNodePasswordKeepsAnExistingOne(t *testing.T) {
	// A password that the machine already registered with must not be
	// replaced. The cluster would refuse the new one.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "password"), []byte("registered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mintNodePassword(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "registered\n" {
		t.Errorf("an existing password was replaced: %q", raw)
	}
}

func TestMintNodePasswordReplacesATornEmptyFile(t *testing.T) {
	// A 0-byte password is a torn write from a power cut, not a
	// credential. Presenting it gets "node password not set" from
	// the cluster forever, so minting a real password is far better.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "password"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mintNodePassword(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Error("the torn password was kept")
	}
}

// tornStateTree builds a clusterState root the way a power cut can
// leave one: healthy identity files beside 0-byte torn files, and a
// valid 0-byte lock file that must survive the sweep.
func tornStateTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"k3s/server/tls/server-ca.crt":                                   "pem bytes",
		"k3s/server/tls/temporary-certs/apiserver-loopback-client__.crt": "",
		"k3s/server/tls/temporary-certs/apiserver-loopback-client__.key": "",
		"k3s/server/cred/passwd":                                         "",
		"k3s/agent/serving-kubelet.crt":                                  "",
		"k3s/agent/kubelet.kubeconfig":                                   "config bytes",
		"k3s/data/.lock":                                                 "",
	}
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSweepTornK3sFilesRemovesTornIdentityFiles(t *testing.T) {
	root := tornStateTree(t)
	sweepTornK3sFiles(root)
	for _, torn := range []string{
		"k3s/server/tls/temporary-certs/apiserver-loopback-client__.crt",
		"k3s/server/tls/temporary-certs/apiserver-loopback-client__.key",
		"k3s/server/cred/passwd",
		"k3s/agent/serving-kubelet.crt",
	} {
		if _, err := os.Stat(filepath.Join(root, torn)); !os.IsNotExist(err) {
			t.Errorf("%s is torn and should be swept", torn)
		}
	}
}

func TestSweepTornK3sFilesKeepsHealthyAndUnrelatedFiles(t *testing.T) {
	root := tornStateTree(t)
	sweepTornK3sFiles(root)
	for _, keep := range []string{
		"k3s/server/tls/server-ca.crt",
		"k3s/agent/kubelet.kubeconfig",
		"k3s/data/.lock",
	} {
		if _, err := os.Stat(filepath.Join(root, keep)); err != nil {
			t.Errorf("%s should survive the sweep: %v", keep, err)
		}
	}
}

func TestSweepTornK3sFilesOnAFreshDiskDoesNothing(t *testing.T) {
	// A first boot has no k3s state yet. The sweep finding nothing is
	// the ordinary case, not an error.
	sweepTornK3sFiles(t.TempDir())
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
