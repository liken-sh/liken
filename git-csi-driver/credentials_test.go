package main

import (
	"fmt"
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
				t.Fatalf("parseCredentials: %v", err)
			}
			switch {
			case c.want == nil && parsed != nil:
				t.Fatalf("parseCredentials answered %+v, want none", *parsed)
			case c.want == nil:
			case parsed == nil:
				t.Fatalf("parseCredentials answered none, want %+v", *c.want)
			case *parsed != *c.want:
				t.Errorf("parseCredentials answered %+v, want %+v", *parsed, *c.want)
			}
		})
	}
}

func TestParseCredentialsRefusesASecretWithNoCredential(t *testing.T) {
	_, err := parseCredentials(map[string]string{"password": "hunter2"})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("parseCredentials answered %v, want %v", got, codes.InvalidArgument)
	}
	want := "nodePublishSecretRef: the Secret carries no ssh-privatekey and no token"
	if got := status.Convert(err).Message(); got != want {
		t.Errorf("parseCredentials said %q, want %q", got, want)
	}
}

func TestUseWritesNothingWithoutASecret(t *testing.T) {
	dir := t.TempDir()
	var none *credentials
	env, remove, err := none.use(dir)
	if err != nil {
		t.Fatalf("use: %v", err)
	}
	remove()
	if len(env) != 0 {
		t.Errorf("use answered %v, want no environment", env)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("use wrote %v", entries)
	}
}

func TestUseWritesThePrivateKeyForTheCallAlone(t *testing.T) {
	dir := t.TempDir()
	holder := &credentials{privateKey: "KEY", knownHosts: "HOSTS", username: defaultUsername}
	env, remove, err := holder.use(dir)
	if err != nil {
		t.Fatalf("use: %v", err)
	}

	key := filepath.Join(dir, privateKeyFile)
	info, err := os.Stat(key)
	if err != nil {
		t.Fatalf("the key is not there: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the key is %v, want 0600", info.Mode().Perm())
	}
	if content, err := os.ReadFile(key); err != nil || string(content) != "KEY\n" {
		t.Errorf("the key holds %q (%v), want %q", content, err, "KEY\n")
	}
	hosts := filepath.Join(dir, knownHostsFile)
	if content, err := os.ReadFile(hosts); err != nil || string(content) != "HOSTS" {
		t.Errorf("the hosts file holds %q (%v), want %q", content, err, "HOSTS")
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
			t.Errorf("%s outlived the call", path)
		}
	}
}

func TestUseAcceptsANewHostWhenTheSecretNamesNone(t *testing.T) {
	holder := &credentials{privateKey: "KEY", username: defaultUsername}
	env, remove, err := holder.use(t.TempDir())
	if err != nil {
		t.Fatalf("use: %v", err)
	}
	defer remove()
	if command := environmentValue(env, "GIT_SSH_COMMAND"); !strings.Contains(command, "StrictHostKeyChecking=accept-new") {
		t.Errorf("GIT_SSH_COMMAND is %q, want accept-new in it", command)
	}
}

func TestUseWritesACredentialHelperForAToken(t *testing.T) {
	dir := t.TempDir()
	holder := &credentials{token: "TOKEN", username: "reader"}
	env, remove, err := holder.use(dir)
	if err != nil {
		t.Fatalf("use: %v", err)
	}

	helper := filepath.Join(dir, helperFile)
	info, err := os.Stat(helper)
	if err != nil {
		t.Fatalf("the helper is not there: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("the helper is %v, want 0700", info.Mode().Perm())
	}
	if got := environmentValue(env, "GIT_CONFIG_VALUE_0"); got != helper {
		t.Errorf("the environment names %q as the helper, want %q", got, helper)
	}

	answer, err := exec.Command(helper, "get").Output()
	if err != nil {
		t.Fatalf("running the helper: %v", err)
	}
	if got := string(answer); got != "username=reader\npassword=TOKEN\n" {
		t.Errorf("the helper answered %q", got)
	}

	// git itself has to read the helper out of the environment, not only
	// the test.
	output, err := runGit(t.Context(), dir, env, "config", "--get", "credential.helper")
	if err != nil {
		t.Fatalf("runGit: %v", err)
	}
	if got := trimLine(output.stdout); got != helper {
		t.Errorf("git read the helper as %q, want %q", got, helper)
	}

	remove()
	if _, err := os.Stat(helper); err == nil {
		t.Error("the helper outlived the call")
	}
}

func TestUseReportsFilesItCannotWrite(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	for _, c := range []struct {
		name   string
		holder *credentials
	}{
		{name: "a private key", holder: &credentials{privateKey: "KEY"}},
		{name: "a token", holder: &credentials{token: "TOKEN", username: "reader"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, remove, err := c.holder.use(gone)
			if err == nil {
				t.Fatal("use answered no error under a directory that is not there")
			}
			remove()
			if env != nil {
				t.Errorf("use answered %v, want no environment", env)
			}
		})
	}
}

func TestUseRemovesTheKeyWhenTheHostsFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, knownHostsFile), 0o755); err != nil {
		t.Fatalf("making the directory in the way: %v", err)
	}
	holder := &credentials{privateKey: "KEY"}
	if _, _, err := holder.use(dir); err == nil {
		t.Fatal("use answered no error with a directory where the hosts file goes")
	}
	if _, err := os.Stat(filepath.Join(dir, privateKeyFile)); err == nil {
		t.Error("the key stayed after the call failed")
	}
}

func TestQuoteCarriesAQuoteThroughAShell(t *testing.T) {
	if got := quote("it's"); got != `'it'\''s'` {
		t.Errorf("quote answered %q", got)
	}
}

func TestEndWithNewlineAddsOneOnlyWhenItIsMissing(t *testing.T) {
	if got := endWithNewline("KEY\n"); got != "KEY\n" {
		t.Errorf("endWithNewline answered %q", got)
	}
	if got := endWithNewline("KEY"); got != "KEY\n" {
		t.Errorf("endWithNewline answered %q", got)
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

// sshdOrSkip starts an sshd of the test's own on a free port, serving
// the user who runs the test, and returns the port and the credentials
// that reach it.
func sshdOrSkip(t *testing.T, dir string) (int, *credentials) {
	t.Helper()
	if _, err := os.Stat(sshdPath); err != nil {
		t.Skipf("the ssh path is not drilled here: %s is not on this machine", sshdPath)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("the ssh path is not drilled here: ssh-keygen is not on this machine")
	}
	who, err := user.Current()
	if err != nil {
		t.Skipf("the ssh path is not drilled here: %v", err)
	}

	for _, name := range []string{"host_key", "user_key"} {
		keygen := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "",
			"-f", filepath.Join(dir, name))
		if out, err := keygen.CombinedOutput(); err != nil {
			t.Skipf("the ssh path is not drilled here: ssh-keygen: %v: %s", err, out)
		}
	}
	port := freePort(t)
	config := strings.Join([]string{
		fmt.Sprintf("Port %d", port),
		"ListenAddress 127.0.0.1",
		"HostKey " + filepath.Join(dir, "host_key"),
		"AuthorizedKeysFile " + filepath.Join(dir, "user_key.pub"),
		"PidFile " + filepath.Join(dir, "sshd.pid"),
		"StrictModes no",
		"UsePAM no",
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"AllowUsers " + who.Username,
		"LogLevel ERROR",
		"",
	}, "\n")
	writeFiles(t, dir, map[string]string{"sshd_config": config})

	daemon := exec.Command(sshdPath, "-f", filepath.Join(dir, "sshd_config"),
		"-E", filepath.Join(dir, "sshd.log"))
	if out, err := daemon.CombinedOutput(); err != nil {
		t.Skipf("the ssh path is not drilled here: sshd: %v: %s", err, out)
	}
	t.Cleanup(func() { stopSSHD(t, filepath.Join(dir, "sshd.pid")) })
	waitForPort(t, port)

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
		t.Fatalf("taking a port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			connection.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nothing answered on 127.0.0.1:%d within 10s", port)
}

func stopSSHD(t *testing.T, pidFile string) {
	t.Helper()
	content, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(content)), "%d", &pid); err != nil {
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = process.Kill()
	_, _ = process.Wait()
}

func TestAPrivateKeyFetchesOverSSH(t *testing.T) {
	dir := t.TempDir()
	port, holder := sshdOrSkip(t, dir)

	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	who, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	url := fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", who.Username, port, source)

	_, repo := storeWith(t, url)
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	env, remove, err := holder.use(dir)
	if err != nil {
		t.Fatalf("use: %v", err)
	}
	defer remove()

	if err := repo.fetch(t.Context(), env, "main", 0); err != nil {
		t.Fatalf("fetch over ssh: %v", err)
	}
	if _, err := repo.resolve(t.Context(), "main"); err != nil {
		t.Errorf("resolve after a fetch over ssh: %v", err)
	}
}

func TestAFetchOverSSHFailsWithoutTheKey(t *testing.T) {
	dir := t.TempDir()
	port, _ := sshdOrSkip(t, dir)

	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	who, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	url := fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", who.Username, port, source)

	_, repo := storeWith(t, url)
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.fetch(t.Context(), nil, "main", 0); err == nil {
		t.Error("a fetch with no credentials answered no error")
	}
}
