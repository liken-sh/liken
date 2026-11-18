package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// registriesTempPath makes the renderer write to a temporary
// directory for one test.
func registriesTempPath(t *testing.T) string {
	t.Helper()
	original := registriesConfigPath
	registriesConfigPath = filepath.Join(t.TempDir(), "registries.yaml")
	t.Cleanup(func() { registriesConfigPath = original })
	return registriesConfigPath
}

func mirroredCluster(embedded bool) *cluster.Cluster {
	return &cluster.Cluster{
		Kind:     "Cluster",
		Metadata: api.ObjectMeta{Name: "lab"},
		Spec: cluster.ClusterSpec{
			Registries: cluster.RegistriesSpec{
				Mirrors:  map[string][]string{"docker.io": {"http://10.10.0.100:5000"}},
				Embedded: embedded,
			},
		},
	}
}

func labCredentials() *machine.RegistryCredentials {
	return &machine.RegistryCredentials{
		APIVersion: api.APIVersion,
		Kind:       "RegistryCredentials",
		Hosts: []machine.RegistryCredential{
			{Host: "10.10.0.100:5000", Username: "liken", Password: "hunter2"},
		},
	}
}

func TestWriteRegistriesConfigRendersMirrorsAndCredentials(t *testing.T) {
	path := registriesTempPath(t)
	store := machine.RegistryCredentialsStore(t.TempDir())

	status := writeRegistriesConfig(mirroredCluster(false), labCredentials(), store, machine.ManifestSourceProven)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registries.yaml: %v", err)
	}
	for _, want := range []string{"docker.io", "http://10.10.0.100:5000", "10.10.0.100:5000", "username: liken", "password: hunter2"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("registries.yaml is\n%s\nwant it to contain %q", raw, want)
		}
	}
	if !slices.Equal(status.Mirrors, []string{"docker.io"}) ||
		!slices.Equal(status.CredentialedHosts, []string{"10.10.0.100:5000"}) || status.Embedded {
		t.Errorf("status is %+v, want mirror docker.io and credentialed host 10.10.0.100:5000", status)
	}
}

func TestWriteRegistriesConfigIsOwnerOnly(t *testing.T) {
	path := registriesTempPath(t)
	writeRegistriesConfig(mirroredCluster(false), labCredentials(), machine.RegistryCredentialsStore(t.TempDir()), machine.ManifestSourceProven)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the mode of registries.yaml: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("registries.yaml has mode %v, want 0600 because it contains passwords", info.Mode().Perm())
	}
}

func TestWriteRegistriesConfigEmbeddedAddsTheWildcard(t *testing.T) {
	path := registriesTempPath(t)
	status := writeRegistriesConfig(mirroredCluster(true), nil, machine.RegistryCredentialsStore(t.TempDir()), "")
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"*"`) && !strings.Contains(string(raw), "'*'") {
		t.Errorf("registries.yaml is\n%s\nwant a \"*\" mirror entry because the embedded registry is on", raw)
	}
	if !status.Embedded || !slices.Equal(status.Mirrors, []string{"*", "docker.io"}) {
		t.Errorf("status is %+v, want Embedded and mirrors \"*\" and docker.io", status)
	}
}

func TestWriteRegistriesConfigEmbeddedKeepsADeclaredWildcard(t *testing.T) {
	path := registriesTempPath(t)
	clusterDoc := mirroredCluster(true)
	clusterDoc.Spec.Registries.Mirrors["*"] = []string{"http://10.10.0.100:5000"}
	writeRegistriesConfig(clusterDoc, nil, machine.RegistryCredentialsStore(t.TempDir()), "")
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "*") != 1 {
		t.Errorf("registries.yaml is\n%s\nwant exactly one \"*\" entry, the declared one", raw)
	}
	if !strings.Contains(string(raw), "http://10.10.0.100:5000") {
		t.Errorf("registries.yaml is\n%s\nwant the endpoints of the declared \"*\" entry", raw)
	}
}

func TestWriteRegistriesConfigCredentialsAloneStillRender(t *testing.T) {
	// Credentials with no mirrors are for Docker Hub's pull rate
	// limit. Pulls go straight to the registry, authenticated, and
	// registries.yaml has only a configs section.
	path := registriesTempPath(t)
	status := writeRegistriesConfig(nil, labCredentials(), machine.RegistryCredentialsStore(t.TempDir()), machine.ManifestSourceProven)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registries.yaml: %v", err)
	}
	if strings.Contains(string(raw), "mirrors") {
		t.Errorf("registries.yaml is\n%s\nwant no mirrors section because none is declared", raw)
	}
	if len(status.CredentialedHosts) != 1 {
		t.Errorf("status is %+v, want one credentialed host", status)
	}
}

func TestWriteRegistriesConfigNothingDeclaredRemovesTheFile(t *testing.T) {
	path := registriesTempPath(t)
	// An earlier rendering exists, and the restart path renders again
	// with nothing declared, as when the last mirror is removed from a
	// running machine.
	if err := os.WriteFile(path, []byte("mirrors: {}\n"), 0o600); err != nil {
		t.Fatalf("writing the earlier registries.yaml: %v", err)
	}
	status := writeRegistriesConfig(nil, nil, machine.RegistryCredentialsStore(t.TempDir()), "")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the earlier registries.yaml still exists, want it removed because nothing is declared")
	}
	if status.Embedded || status.Mirrors != nil || status.CredentialedHosts != nil {
		t.Errorf("status is %+v, want an empty status because nothing is declared", status)
	}
}

func TestWriteRegistriesConfigDeterministicBytes(t *testing.T) {
	path := registriesTempPath(t)
	clusterDoc := mirroredCluster(true)
	clusterDoc.Spec.Registries.Mirrors["quay.io"] = []string{"http://10.10.0.100:5000"}
	store := machine.RegistryCredentialsStore(t.TempDir())

	writeRegistriesConfig(clusterDoc, labCredentials(), store, machine.ManifestSourceProven)
	first, _ := os.ReadFile(path)
	writeRegistriesConfig(clusterDoc, labCredentials(), store, machine.ManifestSourceProven)
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("two renderings of the same inputs differ:\n%s\n%s\nwant identical bytes", first, second)
	}
}

func TestWriteRegistriesConfigPromotesStagedCredentialsOnSuccess(t *testing.T) {
	registriesTempPath(t)
	store := machine.RegistryCredentialsStore(t.TempDir())
	raw, _, err := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err != nil {
		t.Fatalf("rendering the credentials: %v", err)
	}
	if err := store.WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged credentials: %v", err)
	}

	writeRegistriesConfig(mirroredCluster(false), labCredentials(), store, machine.ManifestSourceStaged)

	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged document still exists after the write, want it promoted")
	}
	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Errorf("the proven credentials are %q, want the credentials that were applied", proven)
	}
}

func TestWriteRegistriesConfigFailedWriteLeavesStagedInPlace(t *testing.T) {
	// The file cannot be written, so the credentials stay staged and
	// the next boot retries.
	original := registriesConfigPath
	registriesConfigPath = filepath.Join(t.TempDir(), "no-such-dir", "registries.yaml")
	t.Cleanup(func() { registriesConfigPath = original })

	store := machine.RegistryCredentialsStore(t.TempDir())
	raw, _, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged credentials: %v", err)
	}

	writeRegistriesConfig(mirroredCluster(false), labCredentials(), store, machine.ManifestSourceStaged)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("the staged document is gone after a failed write, want it kept")
	}
}

func TestChooseRegistryCredentialsPrefersStaged(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	provenRaw, _, _ := machine.RenderRegistryCredentials(nil)
	stagedRaw, stagedHash, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteProven(provenRaw); err != nil {
		t.Fatalf("writing the proven credentials: %v", err)
	}
	if err := store.WriteStaged(stagedRaw); err != nil {
		t.Fatalf("writing the staged credentials: %v", err)
	}

	boot := machine.BootStatus{}
	creds := chooseRegistryCredentials(root, true, &boot)
	if creds == nil || len(creds.Hosts) != 1 {
		t.Fatalf("the chosen credentials are %+v, want the one staged host", creds)
	}
	if boot.CredentialsSource != machine.ManifestSourceStaged || boot.CredentialsHash != stagedHash {
		t.Errorf("the boot record is %+v, want the staged source and hash", boot)
	}
}

func TestChooseRegistryCredentialsRejectsAGarbageStagedDocument(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	provenRaw, provenHash, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteProven(provenRaw); err != nil {
		t.Fatalf("writing the proven credentials: %v", err)
	}
	if err := store.WriteStaged([]byte("kind: Nonsense\n")); err != nil {
		t.Fatalf("writing the staged credentials: %v", err)
	}

	boot := machine.BootStatus{}
	creds := chooseRegistryCredentials(root, true, &boot)
	if creds == nil || boot.CredentialsSource != machine.ManifestSourceProven || boot.CredentialsHash != provenHash {
		t.Errorf("credentials are %+v and the boot record is %+v, want the proven credentials after a staged document that does not parse", creds, boot)
	}
	if boot.CredentialsRejection == nil {
		t.Error("the boot record has no rejection, want one for the staged document that does not parse")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged document that does not parse is still staged, want it rejected")
	}
}

func TestChooseRegistryCredentialsNothingAnywhereIsNil(t *testing.T) {
	boot := machine.BootStatus{}
	creds := chooseRegistryCredentials(t.TempDir(), true, &boot)
	if creds != nil || boot.CredentialsSource != "" || boot.CredentialsHash != "" {
		t.Errorf("credentials are %+v and the boot record is %+v, want none because the machine never had credentials", creds, boot)
	}
}

func TestChooseRegistryCredentialsMemoryBackedReadsNothing(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	raw, _, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged credentials: %v", err)
	}
	boot := machine.BootStatus{}
	if creds := chooseRegistryCredentials(root, false, &boot); creds != nil {
		t.Error("a memory-backed machine chose credentials, want none because it has no durable store")
	}
}

func TestChooseRegistryCredentialsReportsAnEarlierRejectionAgain(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	if err := store.WriteStaged([]byte("garbage")); err != nil {
		t.Fatalf("writing the staged credentials: %v", err)
	}
	if err := store.Reject(machine.Rejection{Hash: "abc", Reason: "would not parse"}); err != nil {
		t.Fatalf("recording the rejection: %v", err)
	}

	boot := machine.BootStatus{}
	chooseRegistryCredentials(root, true, &boot)
	if boot.CredentialsRejection == nil || boot.CredentialsRejection.Hash != "abc" {
		t.Errorf("the boot record has rejection %+v, want the earlier rejection abc because the store keeps it across boots", boot.CredentialsRejection)
	}
}

func TestChooseRegistryCredentialsToleratesUnreadableStoreFiles(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	if err := store.WriteStaged([]byte("staged")); err != nil {
		t.Fatalf("writing the staged credentials: %v", err)
	}
	if err := store.WriteProven([]byte("proven")); err != nil {
		t.Fatalf("writing the proven credentials: %v", err)
	}
	sealed, err := filepath.Glob(filepath.Join(root, "*", "*.yaml"))
	if err != nil || len(sealed) == 0 {
		t.Fatalf("finding the store files: %v, %v, want at least one file", sealed, err)
	}
	for _, path := range sealed {
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("making the store file unreadable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	}

	boot := machine.BootStatus{}
	if creds := chooseRegistryCredentials(root, true, &boot); creds != nil {
		t.Errorf("credentials are %+v, want none because the store files are unreadable", creds)
	}
}

func TestChooseRegistryCredentialsFallsThroughACorruptProvenDocument(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	if err := store.WriteProven([]byte("not a credentials document")); err != nil {
		t.Fatalf("writing the proven credentials: %v", err)
	}

	boot := machine.BootStatus{}
	if creds := chooseRegistryCredentials(root, true, &boot); creds != nil {
		t.Errorf("credentials are %+v, want none because the proven document does not parse", creds)
	}
	if boot.CredentialsSource != "" {
		t.Errorf("the boot record is %+v, want no source because no credentials were chosen", boot)
	}
}
