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
		t.Fatalf("rendering the credentials: %v", err)
	}
	backward, backwardHash, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "b.example", Username: "b", Password: "pb"},
		{Host: "a.example", Username: "a", Password: "pa"},
	})
	if err != nil {
		t.Fatalf("rendering the credentials: %v", err)
	}
	if string(forward) != string(backward) || forwardHash != backwardHash {
		t.Errorf("rendering hosts in two orders gives different documents:\n%s\n%s\nwant the same bytes and hash", forward, backward)
	}
}

func TestRenderRegistryCredentialsEmptyIsARealDocument(t *testing.T) {
	// A deleted Secret stages the empty document. It needs a real
	// hash so the lifecycle can tell it from a machine that never had
	// credentials.
	raw, hash, err := RenderRegistryCredentials(nil)
	if err != nil {
		t.Fatalf("rendering the credentials: %v", err)
	}
	if len(raw) == 0 || hash == "" {
		t.Errorf("rendering no credentials gives %d bytes and hash %q, want bytes and a hash", len(raw), hash)
	}
	parsed, err := ParseRegistryCredentials(raw)
	if err != nil {
		t.Fatalf("parsing the credentials: %v", err)
	}
	if len(parsed.Hosts) != 0 {
		t.Errorf("the empty document has hosts %+v, want none", parsed.Hosts)
	}
}

func TestRegistryCredentialsRoundTrip(t *testing.T) {
	raw, _, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "mirror.example:5000", Username: "puller", Password: "hunter2"},
	})
	if err != nil {
		t.Fatalf("rendering the credentials: %v", err)
	}
	parsed, err := ParseRegistryCredentials(raw)
	if err != nil {
		t.Fatalf("parsing the credentials: %v", err)
	}
	if len(parsed.Hosts) != 1 || parsed.Hosts[0].Host != "mirror.example:5000" ||
		parsed.Hosts[0].Username != "puller" || parsed.Hosts[0].Password != "hunter2" {
		t.Errorf("the parsed credentials are %+v, want the one rendered for mirror.example:5000", parsed.Hosts)
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
				t.Fatal("parsing succeeded, want an error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wants)) {
				t.Errorf("the error is %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

func TestRegistryCredentialsStoreIsItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	store := RegistryCredentialsStore(root)
	if err := store.WriteStaged([]byte("creds")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "registries", "staged.yaml")); err != nil {
		t.Errorf("reading registries/staged.yaml under the root: %v", err)
	}
}

func TestCredentialsFilesAreOwnerOnly(t *testing.T) {
	// The staged file holds passwords and gets mode 0600 from the temp
	// file that WriteDurable creates. This test fails if that changes.
	store := RegistryCredentialsStore(t.TempDir())
	if err := store.WriteStaged([]byte("creds")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	info, err := os.Stat(filepath.Join(store.dir, "staged.yaml"))
	if err != nil {
		t.Fatalf("reading the file mode: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the staged file has mode %v, want 0600", info.Mode().Perm())
	}
}
