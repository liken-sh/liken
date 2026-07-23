package main

// The deploy key minter: the cluster operator's part of the flux
// feature.
//
// The flux feature syncs the fleet's declared state from a git
// repository, and that repository is expected to be private, so the
// sync engine needs a credential. liken mints that credential inside
// the cluster, instead of asking a person to generate a key and
// carry it in. The person's whole job is to copy the public half
// from the Cluster's status and register it at the forge as a deploy
// key. Private material never travels, never sits on a USB stick,
// and never depends on someone getting key formats right by hand.
//
// The key is one per cluster, not one per machine, because a
// narrower key would not narrow anything: the private half lives in
// a Secret, every Secret lives in the cluster datastore, and every
// leader carries that datastore. The datastore is the unit of
// exposure, so one key per datastore states the truth. Rotation is a
// person's decision, made by deleting the Secret; the next sweep
// mints a fresh pair and publishes the new half to register.
//
// This job belongs to the cluster operator because the credential is
// cluster-scoped: the sweep is the one writer of Cluster status, and
// the flux-system Secret is a cluster-level object that any leader's
// sweep may create. Init cannot do it, because init runs before k3s
// exists. The permission to touch the Secret arrives with the
// feature itself: the flux feature's manifests carry a Role in
// flux-system (flux/manifests/flux-system.yaml), seeded only while
// the feature is declared, so this operator holds no standing Secret
// access on fleets that never asked for GitOps.

import (
	"crypto/ed25519"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"golang.org/x/crypto/ssh"
)

// The Secret's home, by Flux's conventions: the flux-system
// namespace, a Secret named flux-system, and the identity /
// identity.pub keys that Flux's git client reads. Following the
// conventions means the sync engine finds the credential with no
// configuration pointing at it.
const (
	fluxSecretPath  = "/api/v1/namespaces/flux-system/secrets/flux-system"
	fluxSecretsPath = "/api/v1/namespaces/flux-system/secrets"
)

// ensureFluxDeployKey makes sure the fleet's deploy key exists when
// the flux feature is declared, and returns the public half for the
// status to carry. It returns "" when the feature is off, and also
// on any API failure, because the sweep runs again and a missing
// status one pass long is cheaper than a wrong one. The first passes
// after the feature turns on can fail here legitimately: the Role
// that grants this operator its Secret access is itself seeded by
// the feature, and k3s may not have applied it yet. The sweep's
// level-triggered loop absorbs that window; nothing here needs to
// wait or retry.
func ensureFluxDeployKey(c *kubernetes.Client, clusterDoc *cluster.Cluster) string {
	if !clusterDoc.FeatureEnabled(cluster.FeatureFlux) {
		return ""
	}
	secret := &kubernetes.Secret{}
	err := c.RequestJSON(http.MethodGet, fluxSecretPath, nil, secret)
	if err == nil {
		return string(secret.Data["identity.pub"])
	}
	if !errors.Is(err, kubernetes.ErrNotFound) {
		fmt.Printf("reading the flux deploy key: %v\n", err)
		return ""
	}

	pub, priv, err := mintDeployKey(clusterDoc.Metadata.Name)
	if err != nil {
		fmt.Printf("minting the flux deploy key: %v\n", err)
		return ""
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      "flux-system",
			"namespace": "flux-system",
		},
		"type": "Opaque",
		// stringData is the write-side convenience: the API server
		// encodes it into data, so this code never handles base64.
		"stringData": map[string]string{
			"identity":     priv,
			"identity.pub": pub,
		},
	})
	if err != nil {
		fmt.Printf("encoding the flux deploy key secret: %v\n", err)
		return ""
	}
	if err := c.RequestJSON(http.MethodPost, fluxSecretsPath, body, nil); err != nil {
		// A conflict means another leader's sweep minted first. That
		// copy's key is the fleet's key; the next pass reads it.
		if !errors.Is(err, kubernetes.ErrConflict) {
			fmt.Printf("creating the flux deploy key secret: %v\n", err)
		}
		return ""
	}
	fmt.Printf("minted the flux deploy key; register the public half from the Cluster's status at the forge\n")
	return pub
}

// mintDeployKey generates the pair: an ed25519 key, the modern SSH
// default, small enough to read aloud and accepted by every current
// forge. The public half is one authorized_keys line, commented with
// the cluster's name so a forge's key list stays legible. The
// private half is OpenSSH PEM, the format Flux's git client parses.
func mintDeployKey(clusterName string) (publicKey, privateKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}
	comment := "liken:" + clusterName
	pemBlock, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	// MarshalAuthorizedKey ends the line with a newline and no
	// comment; the status reads better with the comment and without
	// the newline.
	line := string(ssh.MarshalAuthorizedKey(sshPub))
	line = line[:len(line)-1] + " " + comment
	return line, string(pem.EncodeToMemory(pemBlock)), nil
}
