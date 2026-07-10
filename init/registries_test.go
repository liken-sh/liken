package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// registriesTempPath points the renderer at a tempdir for one test.
func registriesTempPath(t *testing.T) string {
	t.Helper()
	original := registriesConfigPath
	registriesConfigPath = filepath.Join(t.TempDir(), "registries.yaml")
	t.Cleanup(func() { registriesConfigPath = original })
	return registriesConfigPath
}

func mirroredCluster(embedded bool) *machine.Cluster {
	return &machine.Cluster{
		Kind:     "Cluster",
		Metadata: machine.ObjectMeta{Name: "lab"},
		Spec: machine.ClusterSpec{
			Registries: machine.RegistriesSpec{
				Mirrors:  map[string][]string{"docker.io": {"http://10.10.0.100:5000"}},
				Embedded: embedded,
			},
		},
	}
}

func labCredentials() *machine.RegistryCredentials {
	return &machine.RegistryCredentials{
		APIVersion: machine.APIVersion,
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
		t.Fatal(err)
	}
	for _, want := range []string{"docker.io", "http://10.10.0.100:5000", "10.10.0.100:5000", "username: liken", "password: hunter2"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("registries.yaml should contain %q:\n%s", want, raw)
		}
	}
	if !slices.Equal(status.Mirrors, []string{"docker.io"}) ||
		!slices.Equal(status.CredentialedHosts, []string{"10.10.0.100:5000"}) || status.Embedded {
		t.Errorf("status should describe the rendering: %+v", status)
	}
}

func TestWriteRegistriesConfigIsOwnerOnly(t *testing.T) {
	path := registriesTempPath(t)
	writeRegistriesConfig(mirroredCluster(false), labCredentials(), machine.RegistryCredentialsStore(t.TempDir()), machine.ManifestSourceProven)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("registries.yaml embeds passwords and must be owner-only, got %v", info.Mode().Perm())
	}
}

func TestWriteRegistriesConfigEmbeddedAddsTheWildcard(t *testing.T) {
	path := registriesTempPath(t)
	status := writeRegistriesConfig(mirroredCluster(true), nil, machine.RegistryCredentialsStore(t.TempDir()), "")
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"*"`) && !strings.Contains(string(raw), "'*'") {
		t.Errorf("embedded should add the wildcard mirror entry:\n%s", raw)
	}
	if !status.Embedded || !slices.Equal(status.Mirrors, []string{"*", "docker.io"}) {
		t.Errorf("got %+v", status)
	}
}

func TestWriteRegistriesConfigEmbeddedKeepsADeclaredWildcard(t *testing.T) {
	path := registriesTempPath(t)
	cluster := mirroredCluster(true)
	cluster.Spec.Registries.Mirrors["*"] = []string{"http://10.10.0.100:5000"}
	writeRegistriesConfig(cluster, nil, machine.RegistryCredentialsStore(t.TempDir()), "")
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "*") != 1 {
		t.Errorf("a declared wildcard must not be duplicated or overwritten:\n%s", raw)
	}
	if !strings.Contains(string(raw), "http://10.10.0.100:5000") {
		t.Errorf("the declared wildcard's endpoints must survive:\n%s", raw)
	}
}

func TestWriteRegistriesConfigCredentialsAloneStillRender(t *testing.T) {
	// Credentials without mirrors is the Docker Hub rate-limit case:
	// no mirror anywhere, but authenticated pulls straight to the
	// registry. Valid registries.yaml, just a configs section.
	path := registriesTempPath(t)
	status := writeRegistriesConfig(nil, labCredentials(), machine.RegistryCredentialsStore(t.TempDir()), machine.ManifestSourceProven)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "mirrors") {
		t.Errorf("no mirrors were declared:\n%s", raw)
	}
	if len(status.CredentialedHosts) != 1 {
		t.Errorf("got %+v", status)
	}
}

func TestWriteRegistriesConfigNothingDeclaredRemovesTheFile(t *testing.T) {
	path := registriesTempPath(t)
	// A previous rendering exists (this is the live-retraction case:
	// the restart path re-renders with nothing declared).
	if err := os.WriteFile(path, []byte("mirrors: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := writeRegistriesConfig(nil, nil, machine.RegistryCredentialsStore(t.TempDir()), "")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a retraction must take the old rendering with it")
	}
	if status.Embedded || status.Mirrors != nil || status.CredentialedHosts != nil {
		t.Errorf("nothing declared renders nothing: %+v", status)
	}
}

func TestWriteRegistriesConfigDeterministicBytes(t *testing.T) {
	path := registriesTempPath(t)
	cluster := mirroredCluster(true)
	cluster.Spec.Registries.Mirrors["quay.io"] = []string{"http://10.10.0.100:5000"}
	store := machine.RegistryCredentialsStore(t.TempDir())

	writeRegistriesConfig(cluster, labCredentials(), store, machine.ManifestSourceProven)
	first, _ := os.ReadFile(path)
	writeRegistriesConfig(cluster, labCredentials(), store, machine.ManifestSourceProven)
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("the same inputs must render the same bytes:\n%s\n%s", first, second)
	}
}

func TestWriteRegistriesConfigPromotesStagedCredentialsOnSuccess(t *testing.T) {
	registriesTempPath(t)
	store := machine.RegistryCredentialsStore(t.TempDir())
	raw, _, err := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}

	writeRegistriesConfig(mirroredCluster(false), labCredentials(), store, machine.ManifestSourceStaged)

	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("promotion should consume the staged document")
	}
	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Errorf("the applied credentials become proven: %q", proven)
	}
}

func TestWriteRegistriesConfigFailedWriteLeavesStagedInPlace(t *testing.T) {
	// The render target is unwritable, so the actuation never
	// happened: staged must remain for the next boot to retry.
	original := registriesConfigPath
	registriesConfigPath = filepath.Join(t.TempDir(), "no-such-dir", "registries.yaml")
	t.Cleanup(func() { registriesConfigPath = original })

	store := machine.RegistryCredentialsStore(t.TempDir())
	raw, _, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}

	writeRegistriesConfig(mirroredCluster(false), labCredentials(), store, machine.ManifestSourceStaged)

	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("a failed write must not promote")
	}
}

func TestChooseRegistryCredentialsPrefersStaged(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	provenRaw, _, _ := machine.RenderRegistryCredentials(nil)
	stagedRaw, stagedHash, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteProven(provenRaw); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged(stagedRaw); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootStatus{}
	creds := chooseRegistryCredentials(root, true, &boot)
	if creds == nil || len(creds.Hosts) != 1 {
		t.Fatalf("staged should win: %+v", creds)
	}
	if boot.CredentialsSource != machine.ManifestSourceStaged || boot.CredentialsHash != stagedHash {
		t.Errorf("the boot record should name the staged copy: %+v", boot)
	}
}

func TestChooseRegistryCredentialsRejectsAGarbageStagedDocument(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	provenRaw, provenHash, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteProven(provenRaw); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged([]byte("kind: Nonsense\n")); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootStatus{}
	creds := chooseRegistryCredentials(root, true, &boot)
	if creds == nil || boot.CredentialsSource != machine.ManifestSourceProven || boot.CredentialsHash != provenHash {
		t.Errorf("a garbage staged document falls back to proven: %+v %+v", creds, boot)
	}
	if boot.CredentialsRejection == nil {
		t.Error("the rejection must be recorded for the facts")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the garbage document should be quarantined, not left staged")
	}
}

func TestChooseRegistryCredentialsNothingAnywhereIsNil(t *testing.T) {
	boot := machine.BootStatus{}
	creds := chooseRegistryCredentials(t.TempDir(), true, &boot)
	if creds != nil || boot.CredentialsSource != "" || boot.CredentialsHash != "" {
		t.Errorf("a machine that never had credentials has none: %+v %+v", creds, boot)
	}
}

func TestChooseRegistryCredentialsMemoryBackedReadsNothing(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	raw, _, _ := machine.RenderRegistryCredentials(labCredentials().Hosts)
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	boot := machine.BootStatus{}
	if creds := chooseRegistryCredentials(root, false, &boot); creds != nil {
		t.Error("a memory-backed machine has no durable store to read")
	}
}

func TestChooseRegistryCredentialsRepublishesAStandingRejection(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	if err := store.WriteStaged([]byte("garbage")); err != nil {
		t.Fatal(err)
	}
	if err := store.Reject(machine.Rejection{Hash: "abc", Reason: "would not parse"}); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootStatus{}
	chooseRegistryCredentials(root, true, &boot)
	if boot.CredentialsRejection == nil || boot.CredentialsRejection.Hash != "abc" {
		t.Errorf("the standing rejection must republish every boot: %+v", boot.CredentialsRejection)
	}
}

func TestChooseRegistryCredentialsToleratesUnreadableStoreFiles(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	if err := store.WriteStaged([]byte("staged")); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteProven([]byte("proven")); err != nil {
		t.Fatal(err)
	}
	sealed, err := filepath.Glob(filepath.Join(root, "*", "*.yaml"))
	if err != nil || len(sealed) == 0 {
		t.Fatalf("expected store files to seal: %v, %v", sealed, err)
	}
	for _, path := range sealed {
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	}

	boot := machine.BootStatus{}
	if creds := chooseRegistryCredentials(root, true, &boot); creds != nil {
		t.Errorf("unreadable stores mean anonymous pulls: %+v", creds)
	}
}

func TestChooseRegistryCredentialsFallsThroughACorruptProvenDocument(t *testing.T) {
	root := t.TempDir()
	store := machine.RegistryCredentialsStore(root)
	if err := store.WriteProven([]byte("not a credentials document")); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootStatus{}
	if creds := chooseRegistryCredentials(root, true, &boot); creds != nil {
		t.Errorf("a corrupt proven document means anonymous pulls, not a crash: %+v", creds)
	}
	if boot.CredentialsSource != "" {
		t.Errorf("nothing was chosen: %+v", boot)
	}
}
