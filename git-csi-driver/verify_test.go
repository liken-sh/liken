package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

// signature is the hex HMAC-SHA256 a forge sends for the body.
func signature(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// headers is one request's headers, named by the test.
func headers(pairs map[string]string) http.Header {
	sent := http.Header{}
	for key, value := range pairs {
		sent.Set(key, value)
	}
	return sent
}

func TestVerifyReadsEachForgesHeader(t *testing.T) {
	body := `{"ref":"refs/heads/main"}`
	for _, c := range []struct {
		name     string
		header   map[string]string
		forge    string
		verified bool
	}{
		{
			name:     "github signs the body with a prefix",
			header:   map[string]string{githubSignatureHeader: "sha256=" + signature(body, "s")},
			forge:    forgeGitHub,
			verified: true,
		},
		{
			name:     "github without the prefix",
			header:   map[string]string{githubSignatureHeader: signature(body, "s")},
			forge:    forgeGitHub,
			verified: false,
		},
		{
			name:     "github with another secret",
			header:   map[string]string{githubSignatureHeader: "sha256=" + signature(body, "other")},
			forge:    forgeGitHub,
			verified: false,
		},
		{
			name:     "gitlab sends the secret itself",
			header:   map[string]string{gitlabTokenHeader: "s"},
			forge:    forgeGitLab,
			verified: true,
		},
		{
			name:     "gitlab sends another secret",
			header:   map[string]string{gitlabTokenHeader: "other"},
			forge:    forgeGitLab,
			verified: false,
		},
		{
			name:     "gitea signs the body with no prefix",
			header:   map[string]string{giteaSignatureHeader: signature(body, "s")},
			forge:    forgeGitea,
			verified: true,
		},
		{
			name:     "gitea signs with another secret",
			header:   map[string]string{giteaSignatureHeader: signature(body, "other")},
			forge:    forgeGitea,
			verified: false,
		},
		{
			name:     "forgejo signs the body with no prefix",
			header:   map[string]string{forgejoSignatureHeader: signature(body, "s")},
			forge:    forgeForgejo,
			verified: true,
		},
		{
			name:     "forgejo signs with another secret",
			header:   map[string]string{forgejoSignatureHeader: signature(body, "other")},
			forge:    forgeForgejo,
			verified: false,
		},
		{
			name:     "no header the driver verifies",
			header:   map[string]string{"X-Some-Forge-Signature": "abc"},
			forge:    "",
			verified: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			forge, verified := verify(headers(c.header), []byte(body), "s")
			if forge != c.forge || verified != c.verified {
				t.Errorf("verify answered %q, %v, want %q, %v",
					forge, verified, c.forge, c.verified)
			}
		})
	}
}

func TestVerifyReadsTheHeadersInOneOrder(t *testing.T) {
	body := `{"ref":"refs/heads/main"}`
	forge, verified := verify(headers(map[string]string{
		githubSignatureHeader: "sha256=" + signature(body, "s"),
		gitlabTokenHeader:     "other",
		giteaSignatureHeader:  "nonsense",
	}), []byte(body), "s")
	if forge != forgeGitHub || !verified {
		t.Errorf("verify answered %q, %v, want %q, true", forge, verified, forgeGitHub)
	}
}
