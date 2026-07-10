package main

// The operator's half of the registry-credentials lifecycle.
//
// Credentials enter the cluster as a kubernetes.io/dockerconfigjson
// Secret at a well-known name (the kubernetes package), because that
// is the shape the whole ecosystem's tooling already produces:
// `kubectl create secret docker-registry` writes one, `docker login`
// writes the same JSON to disk, and imagePullSecrets consume it. The
// operator reads that Secret each pass, renders it into liken's
// canonical credentials document, and stages the rendering onto
// machineState whenever it differs from what the boot actuated —
// the same drift-and-stage loop every other document rides.
//
// The whole pipeline is restart-class: credentials land in
// registries.yaml, which k3s reads only at process start, so the
// staged document converges by bouncing k3s in place, never by
// rebooting the machine. And unlike the cluster document, no
// canonicalization pass is needed before comparing hashes: the
// operator is this document's only author (no image carries a seed,
// no person hand-writes one), so the facts' hash and the desired
// rendering are always outputs of the same function.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/chrisguidry/liken/kubernetes"
	"github.com/chrisguidry/liken/machine"
)

// dockerConfig is the .dockerconfigjson payload: registry host to
// login. Docker's own format, reduced to the fields liken reads.
type dockerConfig struct {
	Auths map[string]dockerAuth `json:"auths"`
}

// dockerAuth is one registry's login. Tools write it two ways:
// explicit username/password fields, or an auth field holding
// base64("username:password"). `docker login` writes the auth form;
// `kubectl create secret docker-registry` writes both.
type dockerAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// desiredRegistryCredentials renders the Secret into the canonical
// credential list: one username/password per registry host, sorted
// by RenderRegistryCredentials downstream. nil means no credentials
// anywhere — the Secret is absent or names no hosts — which is the
// ordinary "nothing declared" state, not an error. A malformed
// Secret is an error, and the message names what to create, because
// the fix is always a corrected Secret.
func desiredRegistryCredentials(secret *kubernetes.Secret) ([]machine.RegistryCredential, error) {
	if secret == nil {
		return nil, nil
	}
	if secret.Type != "kubernetes.io/dockerconfigjson" {
		return nil, fmt.Errorf("the registry-credentials Secret has type %q; create it with `kubectl create secret docker-registry registry-credentials -n liken-system ...` so it carries type kubernetes.io/dockerconfigjson", secret.Type)
	}
	raw, ok := secret.Data[".dockerconfigjson"]
	if !ok {
		return nil, fmt.Errorf("the registry-credentials Secret carries no .dockerconfigjson key; create it with `kubectl create secret docker-registry`")
	}
	var config dockerConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("the registry-credentials Secret's .dockerconfigjson is not valid JSON: %v", err)
	}

	var hosts []machine.RegistryCredential
	for key, auth := range config.Auths {
		username, password := auth.Username, auth.Password
		if username == "" && auth.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(auth.Auth)
			if err != nil {
				return nil, fmt.Errorf("the auth for %s is not valid base64: %v", key, err)
			}
			var found bool
			username, password, found = strings.Cut(string(decoded), ":")
			if !found {
				return nil, fmt.Errorf("the auth for %s does not decode to username:password", key)
			}
		}
		if username == "" {
			return nil, fmt.Errorf("the entry for %s carries no username, in neither the username field nor the auth field", key)
		}
		hosts = append(hosts, machine.RegistryCredential{
			Host:     registryHost(key),
			Username: username,
			Password: password,
		})
	}
	if len(hosts) == 0 {
		return nil, nil
	}
	return hosts, nil
}

// registryHost reduces an auths key to the host containerd matches
// against. Keys are usually bare hosts already, but `docker login`
// records Docker Hub as the URL https://index.docker.io/v1/, so a
// key carrying a scheme is reduced to its host, and index.docker.io
// maps to docker.io — the name image references, and therefore
// registries.yaml, use for the Hub. This is the one normalization
// worth doing, because it is the shape the ecosystem's own tool
// produces.
func registryHost(key string) string {
	host := key
	if strings.Contains(key, "://") {
		if u, err := url.Parse(key); err == nil && u.Host != "" {
			host = u.Host
		}
	}
	if host == "index.docker.io" {
		return "docker.io"
	}
	return host
}

// registriesInputs is one pass's view of the credentials Secret,
// gathered by convergeRegistryCredentials so the decision below
// stays pure.
type registriesInputs struct {
	fetchErr error                        // the API read failed (not absence: absence is desired == nil)
	parseErr error                        // the Secret exists but is malformed
	desired  []machine.RegistryCredential // nil when absent or empty
}

// convergeRegistryCredentials is the credentials document's part of
// one reconcile pass: read the registry-credentials Secret, load this
// machine's durable rejection and staged copy from the store, and
// decide. Credentials converge through the same machinery as the
// other documents, from a different source: the Secret rather than a
// CRD. Only a cluster member carries credentials at all — a machine
// with no cluster document has no operator-authored documents of any
// kind.
func convergeRegistryCredentials(c *kubernetes.Client, store machine.ManifestStore, m *machine.Machine, facts *machine.MachineStatus, t turn) convergence {
	rejection, _ := store.LoadRejection()
	in := registriesInputs{}
	if secret, fetchErr := kubernetes.GetRegistryCredentialsSecret(c); fetchErr != nil {
		in.fetchErr = fetchErr
	} else {
		in.desired, in.parseErr = desiredRegistryCredentials(secret)
	}
	return decideRegistriesConvergence(in, m, facts, rejection, readStagedHash(store), t)
}

// decideRegistriesConvergence is the credentials document's
// convergence decision, mirroring the other documents' short-circuit
// order. The cases:
//
//  1. No facts, or facts without a boot record: Unknown.
//  2. The Secret is unreadable (an API failure, not absence):
//     Unknown, the same posture ClusterUnavailable takes.
//  3. The Secret is malformed: CredentialsInvalid, and nothing is
//     staged — the last good rendering keeps running and the message
//     names the Secret to fix. Report, don't wedge.
//  4. Nothing declared anywhere (no Secret, and this boot rendered
//     no credentials): converged. This case is what keeps a machine
//     that has never had credentials from staging an empty document
//     and restarting once for nothing. It also withdraws a stale
//     staged copy and clears a spent rejection, like every document.
//  5. The boot rendered exactly the desired credentials: converged.
//  6. The desired rendering is the one init rejected: hold.
//  7. machineState is memory-backed: nowhere durable to stage.
//  8. Drift: stage (a deleted Secret stages the *empty* document,
//     the retraction rendering) and gate the k3s restart through
//     policy and the conductor's turn. Always a restart, never a
//     reboot: credentials only touch registries.yaml.
func decideRegistriesConvergence(in registriesInputs, m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return factsIncomplete("CredentialsConverged")
	}
	if in.fetchErr != nil {
		return convergence{condition: convergenceUnknown("CredentialsConverged", "SecretUnavailable",
			fmt.Sprintf("reading the registry-credentials Secret: %v", in.fetchErr))}
	}
	if in.parseErr != nil {
		return convergence{condition: notConverged("CredentialsConverged", "CredentialsInvalid",
			fmt.Sprintf("the registry-credentials Secret is malformed, so the machine keeps its last good credentials: %v", in.parseErr))}
	}

	if in.desired == nil && facts.Boot.CredentialsHash == "" {
		return convergedWithCleanup(
			converged("CredentialsConverged", "NothingDeclared", "no registry credentials are declared"),
			stagedHash, rejection)
	}

	manifest, hash, err := machine.RenderRegistryCredentials(in.desired)
	if err != nil {
		return convergence{condition: notConverged("CredentialsConverged", "StagingFailed", err.Error())}
	}

	if hash == facts.Boot.CredentialsHash {
		return convergedWithCleanup(
			converged("CredentialsConverged", "Converged", fmt.Sprintf("this machine runs the current registry credentials (%d hosts)", len(in.desired))),
			stagedHash, rejection)
	}

	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("CredentialsConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this exact credentials document: %s; edit the Secret to something different", rejection.Reason))}
	}
	if facts.Storage.MachineState.Backing != machine.BackingPartition {
		return machineStateEphemeral("CredentialsConverged", "credentials")
	}

	c := convergence{
		manifest: manifest,
		hash:     hash,
		stage:    stagedHash != hash,
	}
	what := fmt.Sprintf("registry credentials for %d hosts", len(in.desired))
	if in.desired == nil {
		what = "the retraction of the registry credentials"
	}
	gateDisruption(&c, "CredentialsConverged", m.Spec.RebootPolicyOrDefault(), t, true,
		fmt.Sprintf("%s staged (%.12s); rebootPolicy is Manual, so reboot the machine to apply (or set rebootPolicy: Auto, which would apply them with just a k3s restart)", what, hash),
		fmt.Sprintf("%s staged (%.12s); waiting for the cluster to grant a turn to apply them by k3s restart", what, hash),
		fmt.Sprintf("k3s restart requested to apply %s (%.12s)", what, hash))
	return c
}
