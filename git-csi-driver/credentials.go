package main

// credentials.go turns a Secret's data into the environment one git
// invocation runs under.

import (
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The keys the driver reads out of a nodePublishSecretRef Secret.
const (
	privateKeyKey = "ssh-privatekey"
	knownHostsKey = "known_hosts"
	tokenKey      = "token"
	usernameKey   = "username"
)

// defaultUsername is the user a token authenticates as when the Secret
// names none. Forges accept any user with a token; git is the custom.
const defaultUsername = "git"

// The file names the credentials take under the volume's directory.
const (
	privateKeyFile = "ssh-privatekey"
	knownHostsFile = "known_hosts"
	helperFile     = "credential-helper"
)

// credentials is what the kubelet passed from the Secret. It stays in
// memory on the volume, so a later fetch can use it, and reaches the
// disk only around a git invocation.
type credentials struct {
	privateKey string
	knownHosts string
	token      string
	username   string
}

// parseCredentials reads the Secret. No Secret is fine. A Secret with
// neither key is refused, because the person who named it meant one.
func parseCredentials(secrets map[string]string) (*credentials, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	parsed := &credentials{
		privateKey: secrets[privateKeyKey],
		knownHosts: secrets[knownHostsKey],
		token:      secrets[tokenKey],
		username:   secrets[usernameKey],
	}
	if parsed.privateKey == "" && parsed.token == "" {
		return nil, status.Errorf(codes.InvalidArgument,
			"nodePublishSecretRef: the Secret carries no %s and no %s", privateKeyKey, tokenKey)
	}
	if parsed.username == "" {
		parsed.username = defaultUsername
	}
	return parsed, nil
}

// use writes the credential files under dir and returns the environment
// that names them and the function that removes them. A private key on
// the node's disk lives no longer than the git invocation that reads it.
// The token goes through a helper script, not the command line, so it
// never appears in the process table.
func (c *credentials) use(dir string) ([]string, func(), error) {
	if c == nil {
		return nil, func() {}, nil
	}
	written := []string{}
	remove := func() {
		for _, path := range written {
			os.Remove(path)
		}
	}

	var env []string
	if c.privateKey != "" {
		key := filepath.Join(dir, privateKeyFile)
		if err := os.WriteFile(key, []byte(endWithNewline(c.privateKey)), 0o600); err != nil {
			remove()
			return nil, func() {}, err
		}
		written = append(written, key)

		hosts := filepath.Join(dir, knownHostsFile)
		if err := os.WriteFile(hosts, []byte(c.knownHosts), 0o600); err != nil {
			remove()
			return nil, func() {}, err
		}
		written = append(written, hosts)
		env = append(env, "GIT_SSH_COMMAND="+sshCommand(key, hosts, c.knownHosts != ""))
	}

	if c.token != "" {
		helper := filepath.Join(dir, helperFile)
		if err := os.WriteFile(helper, []byte(credentialHelper(c.username, c.token)), 0o700); err != nil {
			remove()
			return nil, func() {}, err
		}
		written = append(written, helper)
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=credential.helper",
			"GIT_CONFIG_VALUE_0="+helper,
		)
	}
	return env, remove, nil
}

// sshCommand makes ssh read the key and the hosts file the driver wrote
// and nothing of the node's own. A Secret with known_hosts can check
// the host key, so that one demands a match. Without it, ssh accepts
// the first key it sees and refuses a later change.
func sshCommand(key, hosts string, knownHosts bool) string {
	checking := "accept-new"
	if knownHosts {
		checking = "yes"
	}
	return strings.Join([]string{
		"ssh",
		"-i", quote(key),
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "UserKnownHostsFile=" + quote(hosts),
		"-o", "StrictHostKeyChecking=" + checking,
	}, " ")
}

// credentialHelper is the script git runs for a password. git reads the
// answer from its standard output.
func credentialHelper(username, token string) string {
	return strings.Join([]string{
		"#!/bin/sh",
		"cat <<'GIT_CSI_CREDENTIAL'",
		"username=" + username,
		"password=" + token,
		"GIT_CSI_CREDENTIAL",
		"",
	}, "\n")
}

// quote makes a path safe inside GIT_SSH_COMMAND, which git splits with
// a shell's rules.
func quote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// endWithNewline appends the newline ssh requires at the end of a key
// file. A Secret authored by hand often lacks it.
func endWithNewline(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}
