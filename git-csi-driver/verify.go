package main

// verify.go checks a push against the Secret the path named. Each
// forge signs its request differently, and the header the request
// carries decides which check runs.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// The header each forge signs with. GitLab sends the configured string
// itself. The other three send an HMAC of the body.
const (
	githubSignatureHeader  = "X-Hub-Signature-256"
	gitlabTokenHeader      = "X-Gitlab-Token"
	giteaSignatureHeader   = "X-Gitea-Signature"
	forgejoSignatureHeader = "X-Forgejo-Signature"
)

// The forge names the log line carries.
const (
	forgeGitHub  = "github"
	forgeGitLab  = "gitlab"
	forgeGitea   = "gitea"
	forgeForgejo = "forgejo"
)

// githubPrefix is what GitHub writes before the hex digest.
const githubPrefix = "sha256="

// verify names the forge whose header the request carries, and reports
// whether the request verified against the secret. A request with none
// of the four headers names no forge and verifies against nothing.
func verify(header http.Header, body []byte, secret string) (string, bool) {
	switch {
	case header.Get(githubSignatureHeader) != "":
		digest, prefixed := strings.CutPrefix(
			header.Get(githubSignatureHeader), githubPrefix)
		return forgeGitHub, prefixed && signedBody(digest, body, secret)
	case header.Get(gitlabTokenHeader) != "":
		return forgeGitLab, sameString(header.Get(gitlabTokenHeader), secret)
	case header.Get(giteaSignatureHeader) != "":
		return forgeGitea, signedBody(header.Get(giteaSignatureHeader), body, secret)
	case header.Get(forgejoSignatureHeader) != "":
		return forgeForgejo, signedBody(header.Get(forgejoSignatureHeader), body, secret)
	}
	return "", false
}

// signedBody compares the hex HMAC-SHA256 of the body in constant time,
// so the time an answer takes says nothing about the secret.
func signedBody(signature string, body []byte, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil))))
}

// sameString compares two strings in constant time, which is what
// GitLab's plain token needs.
func sameString(sent, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(sent), []byte(secret)) == 1
}
