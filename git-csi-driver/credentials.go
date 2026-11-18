package main

// credentials.go turns the data of a Secret into the environment for
// one git invocation.

import (
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The keys that the driver reads from the Secret that
// nodePublishSecretRef names.
const (
	privateKeyKey = "ssh-privatekey"
	knownHostsKey = "known_hosts"
	tokenKey      = "token"
	usernameKey   = "username"
)

// defaultUsername is the user that a token authenticates as when the
// Secret sets none. Forges accept any user with a token, and git is the
// conventional choice.
const defaultUsername = "git"

// The names of the credential files, written under the volume's
// directory.
const (
	privateKeyFile = "ssh-privatekey"
	knownHostsFile = "known_hosts"
	helperFile     = "credential-helper"
)

// credentials is the data from the Secret that the kubelet passed. The
// volume keeps it in memory so that later fetches can use it. It
// reaches the disk only while one git invocation runs.
type credentials struct {
	privateKey string
	knownHosts string
	token      string
	username   string
}

// parseCredentials reads the Secret's data. No Secret returns nil
// credentials. A Secret with neither a private key nor a token is
// refused, because whoever referenced it meant to authenticate.
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
			"nodePublishSecretRef: the Secret has neither %s nor %s", privateKeyKey, tokenKey)
	}
	if parsed.username == "" {
		parsed.username = defaultUsername
	}
	return parsed, nil
}

// use writes the credential files under dir. It returns the environment
// variables that point git at them and a function that deletes them,
// which the caller runs when the git invocation ends. The token goes
// through a helper script and not the command line, so it never appears
// in the process list.
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

// sshCommand returns a GIT_SSH_COMMAND that uses only the key and the
// known_hosts file that the driver wrote, and nothing of the node's
// own. When the Secret has known_hosts, ssh requires the host key to
// match. Without it, ssh accepts any host key, because use writes an
// empty known_hosts file for each invocation and deletes it after.
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

// credentialHelper returns a shell script for git's credential.helper.
// It prints the username and password on standard output, where git
// reads them.
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

// quote single-quotes a path for GIT_SSH_COMMAND, which git splits by
// shell rules.
func quote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// endWithNewline appends the newline that ssh requires at the end of a
// key file. A Secret written by hand often lacks it.
func endWithNewline(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}
