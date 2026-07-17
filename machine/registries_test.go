package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRegistryCredentialsIsDeterministic(t *testing.T) {
	forward, forwardHash, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "a.example", Username: "a", Password: "pa"},
		{Host: "b.example", Username: "b", Password: "pb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	backward, backwardHash, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "b.example", Username: "b", Password: "pb"},
		{Host: "a.example", Username: "a", Password: "pa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(forward) != string(backward) || forwardHash != backwardHash {
		t.Errorf("host order must not change the rendering:\n%s\n%s", forward, backward)
	}
}

func TestRenderRegistryCredentialsEmptyIsARealDocument(t *testing.T) {
	// The empty document is the retraction rendering: a Secret that
	// was deleted stages this, and it must have a real hash so the
	// lifecycle can tell it apart from "never had credentials".
	raw, hash, err := RenderRegistryCredentials(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || hash == "" {
		t.Error("the empty credentials document must still render bytes and a hash")
	}
	parsed, err := ParseRegistryCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Hosts) != 0 {
		t.Errorf("the empty document should carry no hosts: %+v", parsed.Hosts)
	}
}

func TestRegistryCredentialsRoundTrip(t *testing.T) {
	raw, _, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "mirror.example:5000", Username: "puller", Password: "hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRegistryCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Hosts) != 1 || parsed.Hosts[0].Host != "mirror.example:5000" ||
		parsed.Hosts[0].Username != "puller" || parsed.Hosts[0].Password != "hunter2" {
		t.Errorf("credentials did not round-trip: %+v", parsed.Hosts)
	}
}

func TestParseRegistryCredentialsRefusals(t *testing.T) {
	cases := map[string]struct {
		raw   string
		wants string
	}{
		"wrong kind": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: Cluster\n",
			wants: "RegistryCredentials",
		},
		"unknown field": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: RegistryCredentials\nsurprise: true\n",
			wants: "surprise",
		},
		"entry without a host": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: RegistryCredentials\nhosts:\n  - username: u\n    password: p\n",
			wants: "host",
		},
		"entry without a username": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: RegistryCredentials\nhosts:\n  - host: a.example\n    password: p\n",
			wants: "username",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRegistryCredentials([]byte(tc.raw))
			if err == nil {
				t.Fatalf("expected a parse error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wants)) {
				t.Errorf("error %q should mention %q", err, tc.wants)
			}
		})
	}
}

func TestRegistryCredentialsStoreIsItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	store := RegistryCredentialsStore(root)
	if err := store.WriteStaged([]byte("creds")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "registries", "staged.yaml")); err != nil {
		t.Errorf("the credentials lifecycle should live under registries/: %v", err)
	}
}

func TestCredentialsFilesAreOwnerOnly(t *testing.T) {
	// The staged file carries passwords, and the design leans on
	// WriteDurable's CreateTemp giving 0600: this test is the tripwire
	// if anyone ever "fixes" those modes.
	store := RegistryCredentialsStore(t.TempDir())
	if err := store.WriteStaged([]byte("creds")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(store.dir, "staged.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials must be owner-only, got %v", info.Mode().Perm())
	}
}
