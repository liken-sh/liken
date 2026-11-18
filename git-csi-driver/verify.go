package main

// verify.go checks a webhook push request against the Secret that the
// request path names. Forges sign requests in different ways, so the
// header that is present selects the check.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// The signature header of each forge. GitLab sends the configured
// secret itself, and the others send an HMAC-SHA256 of the body.
const (
	githubSignatureHeader  = "X-Hub-Signature-256"
	gitlabTokenHeader      = "X-Gitlab-Token"
	giteaSignatureHeader   = "X-Gitea-Signature"
	forgejoSignatureHeader = "X-Forgejo-Signature"
)

// The forge names that verify returns for log lines.
const (
	forgeGitHub  = "github"
	forgeGitLab  = "gitlab"
	forgeGitea   = "gitea"
	forgeForgejo = "forgejo"
)

const githubPrefix = "sha256="

// verify returns the forge whose header the request has, and whether
// the request passed that forge's check. A request with none of these
// headers returns an empty forge and false.
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

// signedBody compares the signature with the hex HMAC-SHA256 of the
// body in constant time, so the response time does not leak the
// expected signature.
func signedBody(signature string, body []byte, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil))))
}

// sameString compares GitLab's token with the secret in constant time.
func sameString(sent, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(sent), []byte(secret)) == 1
}
