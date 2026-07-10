package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// dockerConfigSecret builds the Secret `kubectl create secret
// docker-registry` would have created, from raw .dockerconfigjson
// bytes.
func dockerConfigSecret(config string) *kubernetes.Secret {
	return &kubernetes.Secret{
		Type: "kubernetes.io/dockerconfigjson",
		Data: map[string][]byte{".dockerconfigjson": []byte(config)},
	}
}

func TestDesiredRegistryCredentialsAbsentSecretIsNothing(t *testing.T) {
	hosts, err := desiredRegistryCredentials(nil)
	if hosts != nil || err != nil {
		t.Errorf("no Secret means no credentials, not an error: %v %v", hosts, err)
	}
}

func TestDesiredRegistryCredentialsReadsUsernamePassword(t *testing.T) {
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(
		`{"auths":{"mirror.example:5000":{"username":"puller","password":"hunter2"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Host != "mirror.example:5000" ||
		hosts[0].Username != "puller" || hosts[0].Password != "hunter2" {
		t.Errorf("got %+v", hosts)
	}
}

func TestDesiredRegistryCredentialsDecodesTheAuthField(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("puller:hunter2"))
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(
		`{"auths":{"mirror.example:5000":{"auth":"` + auth + `"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Username != "puller" || hosts[0].Password != "hunter2" {
		t.Errorf("got %+v", hosts)
	}
}

func TestDesiredRegistryCredentialsNormalizesDockerHub(t *testing.T) {
	// `docker login` records the Hub under its legacy URL; containerd
	// and image references call it docker.io.
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(
		`{"auths":{"https://index.docker.io/v1/":{"username":"u","password":"p"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Host != "docker.io" {
		t.Errorf("got %+v", hosts)
	}
}

func TestDesiredRegistryCredentialsRenderDeterministically(t *testing.T) {
	// The map iteration order here is whatever Go deals; determinism
	// belongs to RenderRegistryCredentials, which sorts. This pins
	// the pair end to end: the same Secret always renders the same
	// document hash.
	secret := dockerConfigSecret(
		`{"auths":{"z.example":{"username":"z","password":"p"},"a.example":{"username":"a","password":"p"}}}`)
	first, err := desiredRegistryCredentials(secret)
	if err != nil {
		t.Fatal(err)
	}
	second, err := desiredRegistryCredentials(secret)
	if err != nil {
		t.Fatal(err)
	}
	_, firstHash, _ := machine.RenderRegistryCredentials(first)
	_, secondHash, _ := machine.RenderRegistryCredentials(second)
	if firstHash != secondHash {
		t.Errorf("the same Secret must render the same document: %s vs %s", firstHash, secondHash)
	}
}

func TestDesiredRegistryCredentialsEmptyAuthsIsNothing(t *testing.T) {
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(`{"auths":{}}`))
	if hosts != nil || err != nil {
		t.Errorf("a Secret naming no hosts is the nothing-declared state: %v %v", hosts, err)
	}
}

func TestDesiredRegistryCredentialsRefusals(t *testing.T) {
	cases := map[string]struct {
		secret *kubernetes.Secret
		wants  string
	}{
		"wrong type": {
			secret: &kubernetes.Secret{Type: "Opaque",
				Data: map[string][]byte{".dockerconfigjson": []byte(`{}`)}},
			wants: "kubectl create secret docker-registry",
		},
		"missing key": {
			secret: &kubernetes.Secret{Type: "kubernetes.io/dockerconfigjson",
				Data: map[string][]byte{"config.json": []byte(`{}`)}},
			wants: ".dockerconfigjson",
		},
		"malformed json": {
			secret: dockerConfigSecret(`{"auths":`),
			wants:  "JSON",
		},
		"garbage auth field": {
			secret: dockerConfigSecret(`{"auths":{"a.example":{"auth":"%%%"}}}`),
			wants:  "a.example",
		},
		"auth without a colon": {
			secret: dockerConfigSecret(`{"auths":{"a.example":{"auth":"` +
				base64.StdEncoding.EncodeToString([]byte("no-colon")) + `"}}}`),
			wants: "a.example",
		},
		"no credentials at all": {
			secret: dockerConfigSecret(`{"auths":{"a.example":{}}}`),
			wants:  "username",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := desiredRegistryCredentials(tc.secret)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q should mention %q", err, tc.wants)
			}
		})
	}
}

// credentialFacts builds the facts of a machine whose boot rendered
// the given credentials hash ("" for a machine that has never had
// credentials).
func credentialFacts(hash string) *machine.MachineStatus {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "cluster-hash")
	facts.Boot.CredentialsHash = hash
	if hash != "" {
		facts.Boot.CredentialsSource = machine.ManifestSourceProven
	}
	return facts
}

func desiredCredentials() []machine.RegistryCredential {
	return []machine.RegistryCredential{
		{Host: "mirror.example:5000", Username: "puller", Password: "hunter2"},
	}
}

func TestCredentialsConvergenceIsUnknownWithoutFacts(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{}, machineWithPolicy(""), nil, nil, "", turnGranted)
	if conv.condition.Status != "Unknown" || conv.condition.Reason != "FactsIncomplete" {
		t.Errorf("got %+v", conv.condition)
	}
}

func TestCredentialsConvergenceIsUnknownWhenTheSecretIsUnreachable(t *testing.T) {
	in := registriesInputs{fetchErr: errors.New("dial tcp: connection refused")}
	conv := decideRegistriesConvergence(in, machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Status != "Unknown" || conv.condition.Reason != "SecretUnavailable" {
		t.Errorf("an API failure is not absence: %+v", conv.condition)
	}
	if conv.stage || conv.requestRestart {
		t.Errorf("nothing may be staged over a failed read: %+v", conv)
	}
}

func TestCredentialsInvalidReportsWithoutStaging(t *testing.T) {
	in := registriesInputs{parseErr: errors.New("the auth for a.example is not valid base64")}
	conv := decideRegistriesConvergence(in, machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Reason != "CredentialsInvalid" || conv.stage || conv.requestRestart {
		t.Errorf("a malformed Secret reports and never stages: %+v", conv)
	}
}

func TestCredentialsNothingDeclaredConverges(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{}, machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Status != "True" || conv.condition.Reason != "NothingDeclared" {
		t.Errorf("got %+v", conv.condition)
	}
	if conv.stage || conv.requestRestart {
		t.Errorf("a machine that never had credentials must not restart for nothing: %+v", conv)
	}
}

func TestCredentialsNothingDeclaredWithdrawsAStaleStagedCopy(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{}, machineWithPolicy(machine.RebootAuto), credentialFacts(""),
		&machine.Rejection{Hash: "old"}, "stale-staged-hash", turnGranted)
	if !conv.withdraw || !conv.clearRejection {
		t.Errorf("an edit taken back should withdraw and clear: %+v", conv)
	}
}

func TestCredentialsConvergedWhenTheBootRendersThem(t *testing.T) {
	desired := desiredCredentials()
	_, hash, _ := machine.RenderRegistryCredentials(desired)
	conv := decideRegistriesConvergence(registriesInputs{desired: desired}, machineWithPolicy(machine.RebootAuto),
		credentialFacts(hash), nil, "", turnGranted)
	if conv.condition.Status != "True" || conv.condition.Reason != "Converged" {
		t.Errorf("got %+v", conv.condition)
	}
}

func TestCredentialsDriftRequestsARestart(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.requestRestart || conv.requestReboot {
		t.Errorf("credentials are restart-class, always: %+v", conv)
	}
	if !conv.stage {
		t.Errorf("the drift must stage: %+v", conv)
	}
}

func TestCredentialsDriftAwaitsItsTurn(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" || conv.requestRestart {
		t.Errorf("restarts take conductor turns: %+v", conv)
	}
}

func TestCredentialsDriftUnderManualPolicyWaits(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(""), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Reason != "RestartPending" || conv.requestRestart {
		t.Errorf("Manual policy stages and waits: %+v", conv)
	}
}

func TestCredentialsRetractionStagesTheEmptyDocument(t *testing.T) {
	// The Secret was deleted, but this boot rendered credentials: the
	// empty document stages, and applying it strips the configs from
	// registries.yaml.
	_, bootHash, _ := machine.RenderRegistryCredentials(desiredCredentials())
	conv := decideRegistriesConvergence(registriesInputs{}, machineWithPolicy(machine.RebootAuto),
		credentialFacts(bootHash), nil, "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.stage {
		t.Errorf("a deleted Secret must stage the retraction: %+v", conv)
	}
	parsed, err := machine.ParseRegistryCredentials(conv.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Hosts) != 0 {
		t.Errorf("the retraction rendering carries no hosts: %+v", parsed.Hosts)
	}
}

func TestCredentialsRejectedLastBootHolds(t *testing.T) {
	desired := desiredCredentials()
	_, hash, _ := machine.RenderRegistryCredentials(desired)
	conv := decideRegistriesConvergence(registriesInputs{desired: desired}, machineWithPolicy(machine.RebootAuto),
		credentialFacts("some-other-hash"), &machine.Rejection{Hash: hash, Reason: "would not parse"}, "", turnGranted)
	if conv.condition.Reason != "RejectedLastBoot" || conv.stage || conv.requestRestart {
		t.Errorf("a rejected document must not be re-staged: %+v", conv)
	}
}

func TestCredentialsRefuseMemoryBackedStaging(t *testing.T) {
	facts := credentialFacts("")
	facts.Storage.MachineState.Backing = machine.BackingMemory
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(machine.RebootAuto), facts, nil, "", turnGranted)
	if conv.condition.Reason != "MachineStateEphemeral" || conv.stage {
		t.Errorf("got %+v", conv)
	}
}

func TestCredentialsStagingIsIdempotent(t *testing.T) {
	desired := desiredCredentials()
	_, hash, _ := machine.RenderRegistryCredentials(desired)
	conv := decideRegistriesConvergence(registriesInputs{desired: desired}, machineWithPolicy(machine.RebootAuto),
		credentialFacts(""), nil, hash, turnGranted)
	if conv.stage {
		t.Error("the exact bytes already wait; staging again is disk churn")
	}
}
