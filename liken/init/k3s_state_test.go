package main

// Tests for k3s's on-disk state: the seed files that init copies onto
// clusterState when it mounts, the datastore that a demoted leader
// leaves behind, the node password, and torn files after a power cut.
// k3s_test.go tests the drop-in rendering and the role derivations.
// The mounts themselves run only under QEMU.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func leaderDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(filepath.Join(db, "etcd", "member"), 0o755); err != nil {
		t.Fatalf("creating the datastore directories: %v", err)
	}
	return db
}

func TestAProvenFollowerPurgesLeaderLeftovers(t *testing.T) {
	db := leaderDB(t)
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Error("the datastore exists after a proven follower boot, want it deleted")
	}
}

func TestAStagedFollowerBootKeepsTheDatastore(t *testing.T) {
	// The trial boot of a manifest that demotes this machine must not
	// delete the datastore. When the manifest fails to prove, the
	// fallback boots the leader role again and needs it.
	db := leaderDB(t)
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceStaged, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("the datastore is gone after a staged follower boot, want it kept")
	}
}

func TestALeaderKeepsItsDatastore(t *testing.T) {
	db := leaderDB(t)
	purgeLeaderLeftovers(api.RoleLeader, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("the datastore is gone after a leader boot, want it kept")
	}
}

// seedRelPaths are the trees that init copies from the image
// onto clusterState. The manifests tree is liken's subdirectory of
// k3s's auto-deploy directory, not the directory itself.
var seedRelPaths = []string{"k3s/server/tls", likenManifestsRel, "k3s/agent/images"}

// fakeSeedSource points seedSourceDir at a temporary copy of the
// image's /var/lib/rancher tree, and returns its path.
func fakeSeedSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range seedRelPaths {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
			t.Fatalf("creating the seed directory %s: %v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel, "seeded"), []byte(rel+"\n"), 0o644); err != nil {
			t.Fatalf("writing the seed file in %s: %v", rel, err)
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
		t.Fatalf("seeding the cluster state: %v", err)
	}
	for _, rel := range seedRelPaths {
		if _, err := os.Stat(filepath.Join(root, rel, "seeded")); err != nil {
			t.Errorf("%s is not seeded, want its files copied: %v", rel, err)
		}
	}
}

func TestSeedClusterStateKeepsIdentityAndRefreshesManifests(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	// The disk already has TLS material and an old manifest tree.
	for _, rel := range []string{"k3s/server/tls", likenManifestsRel} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatalf("creating %s: %v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(root, rel, "existing"), []byte("mine\n"), 0o644); err != nil {
			t.Fatalf("writing the existing file in %s: %v", rel, err)
		}
	}
	if err := seedClusterState(root); err != nil {
		t.Fatalf("seeding the cluster state: %v", err)
	}
	// The TLS material is the machine's identity, so the disk's copy wins.
	if _, err := os.Stat(filepath.Join(root, "k3s/server/tls", "existing")); err != nil {
		t.Error("the existing TLS file is gone, want it kept")
	}
	if _, err := os.Stat(filepath.Join(root, "k3s/server/tls", "seeded")); err == nil {
		t.Error("the image's TLS seed was copied over existing TLS material, want it skipped")
	}
	// The manifests come from the running image, so they are replaced whole.
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "existing")); err == nil {
		t.Error("the old manifest is still there, want it replaced by the image's manifests")
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "seeded")); err != nil {
		t.Error("the image's manifest is missing, want it copied on every boot")
	}
}

// k3s writes the manifests of its own components at the top of the
// auto-deploy directory. When a boot disables a component, k3s reads
// that component's file at startup to tear it down. So seeding must
// replace only liken's subdirectory and leave k3s's files in place.
func TestSeedClusterStateLeavesK3sFilesWhereK3sPutThem(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	top := filepath.Join(root, k3sManifestsRel)
	if err := os.MkdirAll(top, 0o755); err != nil {
		t.Fatalf("creating the auto-deploy directory: %v", err)
	}
	k3sFiles := map[string]string{
		"traefik.yaml":             "kind: HelmChart\n",
		"metrics-server.yaml.skip": "",
		"ccm.yaml":                 "kind: DaemonSet\n",
	}
	for name, content := range k3sFiles {
		if err := os.WriteFile(filepath.Join(top, name), []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	if err := seedClusterState(root); err != nil {
		t.Fatalf("seeding the cluster state: %v", err)
	}

	for name, content := range k3sFiles {
		raw, err := os.ReadFile(filepath.Join(top, name))
		if err != nil {
			t.Errorf("k3s's %s is gone after seeding, want it kept: %v", name, err)
			continue
		}
		if string(raw) != content {
			t.Errorf("k3s's %s is %q, want %q", name, raw, content)
		}
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "seeded")); err != nil {
		t.Errorf("liken's manifest subdirectory is not seeded, want it refreshed: %v", err)
	}
}

// fakeFeatureManifests points featuresDir at a temporary directory
// with one feature's workload manifest, which seeding must recognize as
// liken's.
func fakeFeatureManifests(t *testing.T, slug, name string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, slug, "manifests"), 0o755); err != nil {
		t.Fatalf("creating the feature manifest directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug, "manifests", name), []byte("kind: DaemonSet\n"), 0o644); err != nil {
		t.Fatalf("writing the feature manifest: %v", err)
	}
	old := featuresDir
	featuresDir = dir
	t.Cleanup(func() { featuresDir = old })
}

// A machine upgraded from a release that wrote liken's manifests at
// the top of the auto-deploy directory still has those files. Each
// declares the same objects as the copy in liken's subdirectory, so
// seeding must delete them and nothing else.
func TestSeedClusterStateSweepsLikenManifestsFromTheTop(t *testing.T) {
	fakeSeedSource(t)
	fakeFeatureManifests(t, "iscsi", "iscsid.yaml")
	root := t.TempDir()
	top := filepath.Join(root, k3sManifestsRel)
	if err := os.MkdirAll(top, 0o755); err != nil {
		t.Fatalf("creating the auto-deploy directory: %v", err)
	}
	// A seeded manifest, a feature's workload, a manifest that a
	// feature renders, and a file that belongs to k3s.
	for _, name := range []string{"seeded", "iscsid.yaml", "flux-sync.yaml", "traefik.yaml"} {
		if err := os.WriteFile(filepath.Join(top, name), []byte("kind: Namespace\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	if err := seedClusterState(root); err != nil {
		t.Fatalf("seeding the cluster state: %v", err)
	}

	for _, name := range []string{"seeded", "iscsid.yaml", "flux-sync.yaml"} {
		if _, err := os.Stat(filepath.Join(top, name)); !os.IsNotExist(err) {
			t.Errorf("%s is still at the top of the auto-deploy directory, want it deleted: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(top, "traefik.yaml")); err != nil {
		t.Errorf("traefik.yaml is gone from the top of the auto-deploy directory, want it kept: %v", err)
	}
}

func TestSeedClusterStateWithoutSeedsIsANoOp(t *testing.T) {
	old := seedSourceDir
	seedSourceDir = filepath.Join(t.TempDir(), "nothing")
	t.Cleanup(func() { seedSourceDir = old })
	root := t.TempDir()

	if err := seedClusterState(root); err != nil {
		t.Fatalf("seeding the cluster state: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading the cluster state root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the root has entries %v, want none when the image has no seeds", entries)
	}
}

func TestPurgeLeaderLeftoversReportsAFailedRemoval(t *testing.T) {
	parent := t.TempDir()
	db := filepath.Join(parent, "db")
	if err := os.MkdirAll(filepath.Join(db, "etcd"), 0o755); err != nil {
		t.Fatalf("creating the datastore directories: %v", err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("making the datastore parent read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceProven, db)
	if _, err := os.Stat(db); err != nil {
		t.Error("the datastore is gone, want it kept when the removal fails")
	}
}

func TestPurgeLeaderLeftoversWithNoDatastoreDoesNothing(t *testing.T) {
	// A follower that was never a leader has no datastore, which is not
	// an error.
	purgeLeaderLeftovers(api.RoleFollower, machine.ManifestSourceProven,
		filepath.Join(t.TempDir(), "absent"))
}

func TestPersistNodePasswordSkipsAMemoryBackedMachine(t *testing.T) {
	// A memory-backed machine keeps nothing across a reboot, so init
	// leaves the password on tmpfs and creates no symlink.
	if err := persistNodePassword(machine.AllRolesInMemory()); err != nil {
		t.Errorf("persistNodePassword returned %v for a memory-backed machine, want nil", err)
	}
}

// When the link to the password directory is already in place, the
// mint must still run, or k3s writes the password itself without a
// sync.
func TestTheNodePasswordIsMintedWhenTheLinkIsAlreadyInPlace(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "rancher", "node")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("creating the parent of the link: %v", err)
	}
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("creating the link: %v", err)
	}
	if err := linkNodePassword(dir, link); err != nil {
		t.Fatalf("linking the node password through the existing link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "password")); err != nil {
		t.Errorf("reading the minted password: %v, want a password minted through the existing link", err)
	}
}

// When something other than the link holds the link's path, k3s would
// keep its password in the RAM root, and the next boot would present a
// password that the control plane never recorded. So the link fails
// the boot.
func TestAPathThatCannotBeLinkedFailsTheBoot(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "rancher", "node")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatalf("creating a directory at the path of the link: %v", err)
	}
	if err := linkNodePassword(dir, link); err == nil {
		t.Errorf("linkNodePassword returned nil with a directory at %s, want an error that fails the boot", link)
	}
}

func TestMintNodePasswordWritesOne(t *testing.T) {
	dir := t.TempDir()
	if err := mintNodePassword(dir); err != nil {
		t.Fatalf("minting the node password: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatalf("reading the node password: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}\n$`).Match(raw) {
		t.Errorf("the password is %q, want 32 hex characters and a newline", raw)
	}
	info, err := os.Stat(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatalf("reading the mode of the node password: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the password mode is %v, want 0600", info.Mode().Perm())
	}
}

func TestMintNodePasswordKeepsAnExistingOne(t *testing.T) {
	// The machine has registered with this password, so a new one would
	// be refused by the cluster.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "password"), []byte("registered\n"), 0o600); err != nil {
		t.Fatalf("writing the existing node password: %v", err)
	}
	if err := mintNodePassword(dir); err != nil {
		t.Fatalf("minting the node password: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatalf("reading the node password: %v", err)
	}
	if string(raw) != "registered\n" {
		t.Errorf("the password is %q, want the existing one kept", raw)
	}
}

func TestMintNodePasswordReplacesATornEmptyFile(t *testing.T) {
	// A power cut can leave the password file empty. If init kept it,
	// the server would refuse the node with "node password not set" on
	// every boot.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "password"), nil, 0o600); err != nil {
		t.Fatalf("writing the empty node password: %v", err)
	}
	if err := mintNodePassword(dir); err != nil {
		t.Fatalf("minting the node password: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "password"))
	if err != nil {
		t.Fatalf("reading the node password: %v", err)
	}
	if len(raw) == 0 {
		t.Error("the password file is empty, want a new password")
	}
}

// tornStateTree builds a clusterState root as a power cut can leave
// it: intact files beside 0-byte torn files, and a 0-byte lock file
// that is valid and must survive the sweep.
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
			t.Fatalf("creating the directory of %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
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
			t.Errorf("%s still exists, want the torn file deleted", torn)
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
			t.Errorf("%s is gone, want it kept by the sweep: %v", keep, err)
		}
	}
}

func TestSweepTornK3sFilesOnAFreshDiskDoesNothing(t *testing.T) {
	// A first boot has no k3s state yet, so the sweep finds nothing and
	// must not fail.
	sweepTornK3sFiles(t.TempDir())
}

// When a manifest or image seed cannot be copied, seedClusterState
// logs the failure, copies the other seeds, and returns no error, so
// the boot continues. A TLS seed that cannot be copied fails the boot.
func TestSeedClusterStateReportsAnUnreadableSeed(t *testing.T) {
	dir := fakeSeedSource(t)
	sealed := filepath.Join(dir, likenManifestsRel, "seeded")
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatalf("making the seed unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o644) })
	root := t.TempDir()

	if err := seedClusterState(root); err != nil {
		t.Errorf("seedClusterState returned %v, want no error when a manifest seed is unreadable", err)
	}

	if _, err := os.Stat(filepath.Join(root, "k3s/agent/images", "seeded")); err != nil {
		t.Errorf("the image seed is missing, want it copied beside the unreadable manifest: %v", err)
	}
}
