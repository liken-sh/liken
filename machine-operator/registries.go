package main

// The operator's half of the registry-credentials lifecycle.
//
// Credentials enter the cluster as a kubernetes.io/dockerconfigjson
// Secret at a well-known name (the kubernetes package), because that
// is the shape the whole ecosystem's tooling already produces.
// `kubectl create secret docker-registry` writes one, `docker login`
// writes the same JSON to disk, and imagePullSecrets consume it. The
// operator reads that Secret on each pass, renders it into liken's
// canonical credentials document, and stages the rendering onto
// machineState whenever it differs from what the boot actuated.
// This is the same drift-and-stage loop every other document uses.
//
// This whole pipeline belongs to the restart class of changes.
// Credentials land in registries.yaml, which k3s reads only when
// its process starts, so the staged document converges by
// restarting k3s in place, never by rebooting the machine. And
// unlike the cluster document, no canonicalization pass is needed
// before comparing hashes. The operator is this document's only
// author (no image carries a seed, and no person hand-writes one),
// so the facts' hash and the desired rendering are always outputs
// of the same function.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// dockerConfig is the .dockerconfigjson payload: registry host
// mapped to login. This is Docker's own format, reduced to the
// fields liken reads.
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
// credential list: one username/password pair per registry host,
// sorted later by RenderRegistryCredentials. A nil result means no
// credentials exist anywhere: the Secret is absent, or it names no
// hosts. This is the ordinary "nothing declared" state, not an
// error. A malformed Secret is an error, and the message names what
// to create, because the fix is always a corrected Secret.
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

// registryHost reduces an auths key to the host that containerd
// matches against. Keys are usually bare hosts already, but `docker
// login` records Docker Hub as the URL https://index.docker.io/v1/,
// so the function reduces a key carrying a scheme to its host, and
// maps index.docker.io to docker.io, the name that image
// references, and therefore registries.yaml, use for the Hub. This
// is the one normalization worth doing, because it matches the
// shape the ecosystem's own tool produces.
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

// convergeRegistryCredentials runs the credentials document's part
// of one reconcile pass. It reads the registry-credentials Secret,
// loads this machine's durable rejection and staged copy from the
// store, and makes the convergence decision. Credentials converge
// through the same machinery as the other documents, from a
// different source: the Secret, rather than a CRD. Only a cluster
// member carries credentials at all. A machine with no cluster
// document has no operator-authored documents of any kind.
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
// convergence decision, following the same order of checks as the
// other documents. The cases:
//
//  1. No facts, or facts with no boot record: Unknown.
//  2. The Secret is unreadable, an API failure rather than absence:
//     Unknown, the same posture ClusterUnavailable takes.
//  3. The Secret is malformed: CredentialsInvalid, and nothing is
//     staged. The last good rendering keeps running, and the
//     message names the Secret to fix. This reports the problem
//     instead of getting stuck.
//  4. Nothing declared anywhere, meaning no Secret and this boot
//     rendered no credentials: converged. This case keeps a machine
//     that has never had credentials from staging an empty document
//     and restarting once for nothing. It also withdraws a stale
//     staged copy and clears a spent rejection, like every document.
//  5. The boot rendered exactly the desired credentials: converged.
//  6. The desired rendering is the one init rejected: hold.
//  7. machineState is backed by memory: there is nowhere durable to
//     stage.
//  8. Drift: stage the document (a deleted Secret stages the empty
//     document, the retraction rendering) and gate the k3s restart
//     through policy and the conductor's turn. This is always a
//     restart, never a reboot, because credentials only touch
//     registries.yaml.
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
