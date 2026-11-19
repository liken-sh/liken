package main

// Registry credentials come from a kubernetes.io/dockerconfigjson
// Secret with a fixed name (see the kubernetes package), because
// `kubectl create secret docker-registry` writes that type and
// `docker login` writes the same JSON to disk. On each pass the
// operator renders the Secret into liken's credentials document and
// stages it in machineState when it differs from what this boot
// applied, as it does for the other documents.
//
// The credentials end up in k3s's registries.yaml, which k3s reads
// only when it starts, so a change applies with a k3s restart and
// never needs a reboot. Only this operator writes the document, so the
// hash in the facts and the hash of the desired document come from
// the same render, and unlike the cluster document they need no
// canonical form before they are compared.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// dockerConfig is the part of .dockerconfigjson that liken reads.
type dockerConfig struct {
	Auths map[string]dockerAuth `json:"auths"`
}

// dockerAuth is one registry's login, in either of two forms: the
// username and password fields, or the auth field, which holds
// base64("username:password"). `docker login` writes the auth field,
// and `kubectl create secret docker-registry` writes both forms.
type dockerAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// desiredRegistryCredentials returns one username and password per
// registry host, in random order, because RenderRegistryCredentials
// sorts the hosts. It returns nil and no error when the Secret is
// absent or has no hosts.
func desiredRegistryCredentials(secret *kubernetes.Secret) ([]machine.RegistryCredential, error) {
	if secret == nil {
		return nil, nil
	}
	if secret.Type != "kubernetes.io/dockerconfigjson" {
		return nil, fmt.Errorf("the registry-credentials Secret has type %q, want kubernetes.io/dockerconfigjson. Create it with kubectl create secret docker-registry registry-credentials -n liken-system", secret.Type)
	}
	raw, ok := secret.Data[".dockerconfigjson"]
	if !ok {
		return nil, errors.New("the registry-credentials Secret has no .dockerconfigjson key. Create it with kubectl create secret docker-registry registry-credentials -n liken-system")
	}
	var config dockerConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("the registry-credentials Secret's .dockerconfigjson does not hold valid JSON: %v", err)
	}

	var hosts []machine.RegistryCredential
	for key, auth := range config.Auths {
		username, password := auth.Username, auth.Password
		if username == "" && auth.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(auth.Auth)
			if err != nil {
				return nil, fmt.Errorf("the auth field of %s is not valid base64: %v", key, err)
			}
			var found bool
			username, password, found = strings.Cut(string(decoded), ":")
			if !found {
				return nil, fmt.Errorf("the auth field of %s does not decode to username:password", key)
			}
		}
		if username == "" {
			return nil, fmt.Errorf("the entry for %s has no username in its username field or its auth field", key)
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

// registryHost converts a key of auths to the registry host that
// containerd matches. A key is usually a bare host, but `docker login`
// records Docker Hub as the URL https://index.docker.io/v1/. So a key
// with a scheme becomes its host, and index.docker.io becomes
// docker.io, the name that image references and registries.yaml use
// for Docker Hub.
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

// registriesInputs is what one pass read from the credentials Secret,
// so that decideRegistriesConvergence does no I/O. fetchErr is set
// only when the read failed, and an absent Secret leaves it nil.
type registriesInputs struct {
	fetchErr error
	parseErr error
	desired  []machine.RegistryCredential
}

// convergeRegistryCredentials runs the credentials part of one
// reconcile pass.
func convergeRegistryCredentials(r *reader, store machine.ManifestStore, m *machine.Machine, facts *machine.MachineStatus, t turn) convergence {
	rejection, _ := store.LoadRejection()
	in := registriesInputs{}
	if secret, fetchErr := r.registryCredentials(); fetchErr != nil {
		in.fetchErr = fetchErr
	} else {
		in.desired, in.parseErr = desiredRegistryCredentials(secret)
	}
	return decideRegistriesConvergence(in, m, facts, rejection, readStagedHash(store), t)
}

// decideRegistriesConvergence decides what to do about the
// credentials document, in the same order of checks as the other
// documents.
//
// A failed read of the Secret makes the condition Unknown, because it
// does not show that the Secret is absent. A malformed Secret stages
// nothing, so the machine keeps its last good credentials. A machine
// that never had credentials and has no Secret is converged, so it
// does not stage an empty document and restart k3s for nothing. A
// deleted Secret on a machine that has credentials stages the empty
// document, which removes them.
func decideRegistriesConvergence(in registriesInputs, m *machine.Machine, facts *machine.MachineStatus, rejection *machine.Rejection, stagedHash string, t turn) convergence {
	if facts == nil || facts.Boot.ManifestSource == "" {
		return factsIncomplete("CredentialsConverged")
	}
	if in.fetchErr != nil {
		return convergence{condition: convergenceUnknown("CredentialsConverged", "SecretUnavailable",
			fmt.Sprintf("The registry-credentials Secret cannot be read: %v", in.fetchErr))}
	}
	if in.parseErr != nil {
		return convergence{condition: notConverged("CredentialsConverged", "CredentialsInvalid",
			fmt.Sprintf("The registry-credentials Secret is not valid, so the machine keeps its last good credentials: %v", in.parseErr))}
	}

	if in.desired == nil && facts.Boot.CredentialsHash == "" {
		return convergedWithCleanup(
			converged("CredentialsConverged", "NothingDeclared", "No registry credentials are declared"),
			stagedHash, rejection)
	}

	manifest, hash, err := machine.RenderRegistryCredentials(in.desired)
	if err != nil {
		return convergence{condition: notConverged("CredentialsConverged", "StagingFailed", "The registry credentials are not staged: "+err.Error())}
	}

	if hash == facts.Boot.CredentialsHash {
		return convergedWithCleanup(
			converged("CredentialsConverged", "Converged", fmt.Sprintf("This machine runs the current registry credentials (%d hosts)", len(in.desired))),
			stagedHash, rejection)
	}

	if rejection != nil && rejection.Hash == hash {
		return convergence{condition: notConverged("CredentialsConverged", "RejectedLastBoot",
			fmt.Sprintf("init rejected this credentials document: %s. The operator does not stage a rejected document again, so change the Secret", rejection.Reason))}
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
		m.Metadata.Annotations[requestApprovalKey],
		what,
		fmt.Sprintf("The %s are staged (%.12s). rebootPolicy is Manual, so reboot the machine to apply them, or set rebootPolicy to Auto, which applies them with a k3s restart", what, hash),
		fmt.Sprintf("The %s are staged (%.12s) and wait for a turn from the cluster to apply them with a k3s restart", what, hash),
		fmt.Sprintf("A k3s restart is requested to apply the %s (%.12s)", what, hash))
	return c
}
