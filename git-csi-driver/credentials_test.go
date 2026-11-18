package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseCredentialsReadsTheSecret(t *testing.T) {
	for _, c := range []struct {
		name    string
		secrets map[string]string
		want    *credentials
	}{
		{
			name:    "no Secret at all",
			secrets: nil,
			want:    nil,
		},
		{
			name:    "a private key",
			secrets: map[string]string{privateKeyKey: "KEY"},
			want:    &credentials{privateKey: "KEY", username: defaultUsername},
		},
		{
			name:    "a private key and the hosts it trusts",
			secrets: map[string]string{privateKeyKey: "KEY", knownHostsKey: "HOSTS"},
			want:    &credentials{privateKey: "KEY", knownHosts: "HOSTS", username: defaultUsername},
		},
		{
			name:    "a token",
			secrets: map[string]string{tokenKey: "TOKEN"},
			want:    &credentials{token: "TOKEN", username: defaultUsername},
		},
		{
			name:    "a token and the user it belongs to",
			secrets: map[string]string{tokenKey: "TOKEN", usernameKey: "reader"},
			want:    &credentials{token: "TOKEN", username: "reader"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			parsed, err := parseCredentials(c.secrets)
			if err != nil {
				t.Fatalf("parsing the credentials: %v", err)
			}
			switch {
			case c.want == nil && parsed != nil:
				t.Fatalf("parseCredentials returned %+v, want none", *parsed)
			case c.want == nil:
			case parsed == nil:
				t.Fatalf("parseCredentials returned none, want %+v", *c.want)
			case *parsed != *c.want:
				t.Errorf("parseCredentials returned %+v, want %+v", *parsed, *c.want)
			}
		})
	}
}

func TestParseCredentialsRefusesASecretWithNoCredential(t *testing.T) {
	_, err := parseCredentials(map[string]string{"password": "hunter2"})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("parseCredentials returned the code %v, want %v", got, codes.InvalidArgument)
	}
	want := "nodePublishSecretRef: the Secret has neither ssh-privatekey nor token"
	if got := status.Convert(err).Message(); got != want {
		t.Errorf("parseCredentials message is %q, want %q", got, want)
	}
}

func TestUseWritesNothingWithoutASecret(t *testing.T) {
	dir := t.TempDir()
	var none *credentials
	env, remove, err := none.use(dir)
	if err != nil {
		t.Fatalf("writing the credential files: %v", err)
	}
	remove()
	if len(env) != 0 {
		t.Errorf("use returned the environment %v, want none", env)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("use wrote %v, want no files", entries)
	}
}

func TestUseWritesThePrivateKeyForTheCallAlone(t *testing.T) {
	dir := t.TempDir()
	supplied := &credentials{privateKey: "KEY", knownHosts: "HOSTS", username: defaultUsername}
	env, remove, err := supplied.use(dir)
	if err != nil {
		t.Fatalf("writing the credential files: %v", err)
	}

	key := filepath.Join(dir, privateKeyFile)
	info, err := os.Stat(key)
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the key is %v, want 0600", info.Mode().Perm())
	}
	if content, err := os.ReadFile(key); err != nil || string(content) != "KEY\n" {
		t.Errorf("the key has %q (%v), want %q", content, err, "KEY\n")
	}
	hosts := filepath.Join(dir, knownHostsFile)
	if content, err := os.ReadFile(hosts); err != nil || string(content) != "HOSTS" {
		t.Errorf("the hosts file has %q (%v), want %q", content, err, "HOSTS")
	}

	command := environmentValue(env, "GIT_SSH_COMMAND")
	for _, want := range []string{key, hosts, "StrictHostKeyChecking=yes", "BatchMode=yes"} {
		if !strings.Contains(command, want) {
			t.Errorf("GIT_SSH_COMMAND is %q, want %q in it", command, want)
		}
	}

	remove()
	for _, path := range []string{key, hosts} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s remains after the call, want it removed", path)
		}
	}
}

func TestUseAcceptsANewHostWhenTheSecretHasNoKnownHosts(t *testing.T) {
	supplied := &credentials{privateKey: "KEY", username: defaultUsername}
	env, remove, err := supplied.use(t.TempDir())
	if err != nil {
		t.Fatalf("writing the credential files: %v", err)
	}
	defer remove()
	if command := environmentValue(env, "GIT_SSH_COMMAND"); !strings.Contains(command, "StrictHostKeyChecking=accept-new") {
		t.Errorf("GIT_SSH_COMMAND is %q, want accept-new in it", command)
	}
}

func TestUseWritesACredentialHelperForAToken(t *testing.T) {
	dir := t.TempDir()
	supplied := &credentials{token: "TOKEN", username: "reader"}
	env, remove, err := supplied.use(dir)
	if err != nil {
		t.Fatalf("writing the credential files: %v", err)
	}

	helper := filepath.Join(dir, helperFile)
	info, err := os.Stat(helper)
	if err != nil {
		t.Fatalf("reading the helper file: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("the helper is %v, want 0700", info.Mode().Perm())
	}
	if got := environmentValue(env, "GIT_CONFIG_VALUE_0"); got != helper {
		t.Errorf("the environment names %q as the helper, want %q", got, helper)
	}

	response, err := exec.Command(helper, "get").Output()
	if err != nil {
		t.Fatalf("running the helper: %v", err)
	}
	if got := string(response); got != "username=reader\npassword=TOKEN\n" {
		t.Errorf("the helper printed %q, want the username and the token", got)
	}

	// Check that git itself reads the helper from the environment, and not
	// only the test code.
	output, err := runGit(t.Context(), dir, env, "config", "--get", "credential.helper")
	if err != nil {
		t.Fatalf("running git with the helper: %v", err)
	}
	if got := trimLine(output.stdout); got != helper {
		t.Errorf("git read the helper as %q, want %q", got, helper)
	}

	remove()
	if _, err := os.Stat(helper); err == nil {
		t.Error("the helper remains after the call, want it removed")
	}
}

func TestUseReportsFilesItCannotWrite(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	for _, c := range []struct {
		name     string
		supplied *credentials
	}{
		{name: "a private key", supplied: &credentials{privateKey: "KEY"}},
		{name: "a token", supplied: &credentials{token: "TOKEN", username: "reader"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, remove, err := c.supplied.use(gone)
			if err == nil {
				t.Fatal("use returned no error under a directory that is not there, want one")
			}
			remove()
			if env != nil {
				t.Errorf("use returned the environment %v, want none", env)
			}
		})
	}
}

func TestUseRemovesTheKeyWhenTheHostsFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, knownHostsFile), 0o755); err != nil {
		t.Fatalf("making the directory in the way: %v", err)
	}
	supplied := &credentials{privateKey: "KEY"}
	if _, _, err := supplied.use(dir); err == nil {
		t.Fatal("use returned no error with a directory where the hosts file goes, want one")
	}
	if _, err := os.Stat(filepath.Join(dir, privateKeyFile)); err == nil {
		t.Error("the key remains after the call returned an error, want it removed")
	}
}

func TestQuoteKeepsAQuoteThroughAShell(t *testing.T) {
	if got := quote("it's"); got != `'it'\''s'` {
		t.Errorf("quote(%q) = %q, want %q", "it's", got, `'it'\''s'`)
	}
}

func TestEndWithNewlineAddsOneOnlyWhenItIsMissing(t *testing.T) {
	if got := endWithNewline("KEY\n"); got != "KEY\n" {
		t.Errorf("endWithNewline(%q) = %q, want %q", "KEY\n", got, "KEY\n")
	}
	if got := endWithNewline("KEY"); got != "KEY\n" {
		t.Errorf("endWithNewline(%q) = %q, want %q", "KEY", got, "KEY\n")
	}
}

// environmentValue is one variable's value in env, or the empty string.
func environmentValue(env []string, name string) string {
	for _, entry := range env {
		if value, found := strings.CutPrefix(entry, name+"="); found {
			return value
		}
	}
	return ""
}

// sshdPath is where Debian and Ubuntu install sshd. A machine without
// it skips the SSH tests and says why.
const sshdPath = "/usr/sbin/sshd"

// sshdOrSkip starts an sshd on a free port that accepts the user who
// runs the test. It returns the port and the credentials that
// authenticate to it.
func sshdOrSkip(t *testing.T, dir string) (int, *credentials) {
	t.Helper()
	if _, err := os.Stat(sshdPath); err != nil {
		t.Skipf("sshd did not start, so the ssh test is skipped: %s is not on this machine", sshdPath)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("sshd did not start, so the ssh test is skipped: ssh-keygen is not on this machine")
	}
	who, err := user.Current()
	if err != nil {
		t.Skipf("sshd did not start, so the ssh test is skipped: %v", err)
	}

	for _, name := range []string{"host_key", "user_key"} {
		keygen := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "",
			"-f", filepath.Join(dir, name))
		if out, err := keygen.CombinedOutput(); err != nil {
			t.Skipf("sshd did not start, so the ssh test is skipped: ssh-keygen: %v: %s", err, out)
		}
	}
	port := freePort(t)
	config := strings.Join([]string{
		fmt.Sprintf("Port %d", port),
		"ListenAddress 127.0.0.1",
		"HostKey " + filepath.Join(dir, "host_key"),
		"AuthorizedKeysFile " + filepath.Join(dir, "user_key.pub"),
		"StrictModes no",
		"UsePAM no",
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"AllowUsers " + who.Username,
		// VERBOSE makes sshd log the line that says it is listening.
		"LogLevel VERBOSE",
		"",
	}, "\n")
	writeFiles(t, dir, map[string]string{"sshd_config": config})

	// sshd runs in the foreground and logs to stderr, so the test starts
	// the process itself and reads its stderr for the line that says it is
	// listening.
	daemon := exec.Command(sshdPath, "-D", "-e", "-f", filepath.Join(dir, "sshd_config"))
	stderr, err := daemon.StderrPipe()
	if err != nil {
		t.Fatalf("opening the stderr pipe of sshd: %v", err)
	}
	if err := daemon.Start(); err != nil {
		t.Skipf("sshd did not start, so the ssh test is skipped: sshd: %v", err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
	})
	if out, listening := sshdListens(daemon, stderr); !listening {
		t.Skipf("sshd did not start, so the ssh test is skipped: sshd: %s", out)
	}

	key, err := os.ReadFile(filepath.Join(dir, "user_key"))
	if err != nil {
		t.Fatalf("reading the user key: %v", err)
	}
	hostKey, err := os.ReadFile(filepath.Join(dir, "host_key.pub"))
	if err != nil {
		t.Fatalf("reading the host key: %v", err)
	}
	return port, &credentials{
		privateKey: string(key),
		knownHosts: fmt.Sprintf("[127.0.0.1]:%d %s", port, hostKey),
		username:   defaultUsername,
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("choosing a port for sshd: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// sshdTimeout kills sshd when it has not logged that it is listening by
// then. A test continues as soon as the line appears and does not wait
// for the timeout.
const sshdTimeout = 30 * time.Second

// sshdListens reads sshd's log until the line that says it is
// listening. It returns false and the log so far when sshd exits first.
// It reads and discards the rest of the log, so sshd never blocks on a
// full pipe.
func sshdListens(daemon *exec.Cmd, stderr io.Reader) (string, bool) {
	bound := time.AfterFunc(sshdTimeout, func() { _ = daemon.Process.Kill() })
	defer bound.Stop()
	lines := bufio.NewScanner(stderr)
	out := []string{}
	for lines.Scan() {
		out = append(out, lines.Text())
		if strings.Contains(lines.Text(), "Server listening on") {
			go func() { _, _ = io.Copy(io.Discard, stderr) }()
			return "", true
		}
	}
	return strings.Join(out, "\n"), false
}

func TestAPrivateKeyFetchesOverSSH(t *testing.T) {
	dir := t.TempDir()
	port, supplied := sshdOrSkip(t, dir)

	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	who, err := user.Current()
	if err != nil {
		t.Fatalf("reading the current user: %v", err)
	}
	url := fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", who.Username, port, source)

	_, repo := storeWith(t, url)
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("creating the repository: %v", err)
	}
	env, remove, err := supplied.use(dir)
	if err != nil {
		t.Fatalf("writing the credential files: %v", err)
	}
	defer remove()

	if err := repo.fetch(t.Context(), env, "main", 0); err != nil {
		t.Fatalf("fetching over ssh: %v", err)
	}
	if _, err := repo.resolve(t.Context(), "main"); err != nil {
		t.Errorf("resolving the ref after a fetch over ssh: %v", err)
	}
}

func TestAFetchOverSSHFailsWithoutTheKey(t *testing.T) {
	dir := t.TempDir()
	port, _ := sshdOrSkip(t, dir)

	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	who, err := user.Current()
	if err != nil {
		t.Fatalf("reading the current user: %v", err)
	}
	url := fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", who.Username, port, source)

	_, repo := storeWith(t, url)
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("creating the repository: %v", err)
	}
	err = repo.fetch(t.Context(), nil, "main", 0)
	if err == nil || !strings.Contains(err.Error(), "Host key verification failed") {
		t.Errorf("a fetch with no credentials returned %v, want the host key refusal of ssh in it", err)
	}
}
