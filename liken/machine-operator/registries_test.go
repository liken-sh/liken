package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// dockerConfigSecret builds the Secret that `kubectl create secret
// docker-registry` creates, from the .dockerconfigjson text.
func dockerConfigSecret(config string) *kubernetes.Secret {
	return &kubernetes.Secret{
		Type: "kubernetes.io/dockerconfigjson",
		Data: map[string][]byte{".dockerconfigjson": []byte(config)},
	}
}

func TestDesiredRegistryCredentialsAbsentSecretIsNothing(t *testing.T) {
	hosts, err := desiredRegistryCredentials(nil)
	if hosts != nil || err != nil {
		t.Errorf("an absent Secret returns %v, %v, want no hosts and no error", hosts, err)
	}
}

func TestDesiredRegistryCredentialsReadsUsernamePassword(t *testing.T) {
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(
		`{"auths":{"mirror.example:5000":{"username":"puller","password":"hunter2"}}}`))
	if err != nil {
		t.Fatalf("reading the credentials of the Secret: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Host != "mirror.example:5000" ||
		hosts[0].Username != "puller" || hosts[0].Password != "hunter2" {
		t.Errorf("the hosts are %+v, want mirror.example:5000 with username puller and password hunter2", hosts)
	}
}

func TestDesiredRegistryCredentialsDecodesTheAuthField(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("puller:hunter2"))
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(
		`{"auths":{"mirror.example:5000":{"auth":"` + auth + `"}}}`))
	if err != nil {
		t.Fatalf("reading the credentials of the Secret: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Username != "puller" || hosts[0].Password != "hunter2" {
		t.Errorf("the hosts are %+v, want username puller and password hunter2", hosts)
	}
}

func TestDesiredRegistryCredentialsNormalizesDockerHub(t *testing.T) {
	// `docker login` records Docker Hub under its legacy URL, and
	// containerd and image references call it docker.io.
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(
		`{"auths":{"https://index.docker.io/v1/":{"username":"u","password":"p"}}}`))
	if err != nil {
		t.Fatalf("reading the credentials of the Secret: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Host != "docker.io" {
		t.Errorf("the hosts are %+v, want the single host docker.io", hosts)
	}
}

func TestDesiredRegistryCredentialsRenderDeterministically(t *testing.T) {
	// Go iterates the auths map in random order.
	secret := dockerConfigSecret(
		`{"auths":{"z.example":{"username":"z","password":"p"},"a.example":{"username":"a","password":"p"}}}`)
	first, err := desiredRegistryCredentials(secret)
	if err != nil {
		t.Fatalf("reading the credentials of the Secret: %v", err)
	}
	second, err := desiredRegistryCredentials(secret)
	if err != nil {
		t.Fatalf("reading the credentials of the Secret: %v", err)
	}
	_, firstHash, _ := machine.RenderRegistryCredentials(first)
	_, secondHash, _ := machine.RenderRegistryCredentials(second)
	if firstHash != secondHash {
		t.Errorf("the same Secret renders hashes %s and %s, want equal hashes", firstHash, secondHash)
	}
}

func TestDesiredRegistryCredentialsEmptyAuthsIsNothing(t *testing.T) {
	hosts, err := desiredRegistryCredentials(dockerConfigSecret(`{"auths":{}}`))
	if hosts != nil || err != nil {
		t.Errorf("a Secret with no hosts returns %v, %v, want no hosts and no error", hosts, err)
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
				t.Fatal("desiredRegistryCredentials returned no error, want a refusal")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("the error is %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

// credentialFacts builds the facts of a machine whose boot applied
// the credentials with the given hash. An empty hash means the boot
// applied none.
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
		t.Errorf("the condition is %+v, want Unknown with reason FactsIncomplete", conv.condition)
	}
}

func TestCredentialsConvergenceIsUnknownWhenTheSecretIsUnreachable(t *testing.T) {
	in := registriesInputs{fetchErr: errors.New("dial tcp: connection refused")}
	conv := decideRegistriesConvergence(in, machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Status != "Unknown" || conv.condition.Reason != "SecretUnavailable" {
		t.Errorf("after a failed read of the Secret the condition is %+v, want Unknown with reason SecretUnavailable", conv.condition)
	}
	if conv.stage || conv.requestRestart {
		t.Errorf("after a failed read of the Secret the convergence is %+v, want no staging and no restart", conv)
	}
}

func TestCredentialsInvalidReportsWithoutStaging(t *testing.T) {
	in := registriesInputs{parseErr: errors.New("the auth field of a.example is not valid base64")}
	conv := decideRegistriesConvergence(in, machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Reason != "CredentialsInvalid" || conv.stage || conv.requestRestart {
		t.Errorf("for a malformed Secret the convergence is %+v, want reason CredentialsInvalid with no staging and no restart", conv)
	}
}

func TestCredentialsNothingDeclaredConverges(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{}, machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Status != "True" || conv.condition.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True with reason NothingDeclared", conv.condition)
	}
	if conv.stage || conv.requestRestart {
		t.Errorf("for a machine that never had credentials the convergence is %+v, want no staging and no k3s restart", conv)
	}
}

func TestCredentialsNothingDeclaredWithdrawsAStaleStagedCopy(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{}, machineWithPolicy(machine.RebootAuto), credentialFacts(""),
		&machine.Rejection{Hash: "old"}, "stale-staged-hash", turnGranted)
	if !conv.withdraw || !conv.clearRejection {
		t.Errorf("after the credentials are removed the convergence is %+v, want it to withdraw the staged copy and clear the rejection", conv)
	}
}

func TestCredentialsConvergedWhenTheBootRendersThem(t *testing.T) {
	desired := desiredCredentials()
	_, hash, _ := machine.RenderRegistryCredentials(desired)
	conv := decideRegistriesConvergence(registriesInputs{desired: desired}, machineWithPolicy(machine.RebootAuto),
		credentialFacts(hash), nil, "", turnGranted)
	if conv.condition.Status != "True" || conv.condition.Reason != "Converged" {
		t.Errorf("the condition is %+v, want True with reason Converged", conv.condition)
	}
}

func TestCredentialsDriftRequestsARestart(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.requestRestart || conv.requestReboot {
		t.Errorf("for changed credentials the convergence is %+v, want reason RestartRequested, a k3s restart, and no reboot", conv)
	}
	if !conv.stage {
		t.Errorf("for changed credentials the convergence is %+v, want it to stage the document", conv)
	}
}

func TestCredentialsDriftAwaitsItsTurn(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(machine.RebootAuto), credentialFacts(""), nil, "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" || conv.requestRestart {
		t.Errorf("before the machine's turn the convergence is %+v, want reason AwaitingTurn and no restart", conv)
	}
}

func TestCredentialsDriftUnderManualPolicyWaits(t *testing.T) {
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(""), credentialFacts(""), nil, "", turnGranted)
	if conv.condition.Reason != "RestartPending" || conv.requestRestart {
		t.Errorf("under the Manual policy the convergence is %+v, want reason RestartPending and no restart", conv)
	}
}

func TestCredentialsRetractionStagesTheEmptyDocument(t *testing.T) {
	// The Secret was deleted, and this boot applied credentials.
	_, bootHash, _ := machine.RenderRegistryCredentials(desiredCredentials())
	conv := decideRegistriesConvergence(registriesInputs{}, machineWithPolicy(machine.RebootAuto),
		credentialFacts(bootHash), nil, "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.stage {
		t.Errorf("after the Secret is deleted the convergence is %+v, want reason RestartRequested and a staged empty document", conv)
	}
	parsed, err := machine.ParseRegistryCredentials(conv.manifest)
	if err != nil {
		t.Fatalf("parsing the staged document: %v", err)
	}
	if len(parsed.Hosts) != 0 {
		t.Errorf("the staged document has hosts %+v, want none", parsed.Hosts)
	}
}

func TestCredentialsRejectedLastBootIsNotStaged(t *testing.T) {
	desired := desiredCredentials()
	_, hash, _ := machine.RenderRegistryCredentials(desired)
	conv := decideRegistriesConvergence(registriesInputs{desired: desired}, machineWithPolicy(machine.RebootAuto),
		credentialFacts("some-other-hash"), &machine.Rejection{Hash: hash, Reason: "would not parse"}, "", turnGranted)
	if conv.condition.Reason != "RejectedLastBoot" || conv.stage || conv.requestRestart {
		t.Errorf("for a document rejected at the last boot the convergence is %+v, want reason RejectedLastBoot and no staging or restart", conv)
	}
}

func TestCredentialsRefuseMemoryBackedStaging(t *testing.T) {
	facts := credentialFacts("")
	facts.Storage.MachineState.Backing = machine.BackingMemory
	conv := decideRegistriesConvergence(registriesInputs{desired: desiredCredentials()},
		machineWithPolicy(machine.RebootAuto), facts, nil, "", turnGranted)
	if conv.condition.Reason != "MachineStateEphemeral" || conv.stage {
		t.Errorf("the convergence is %+v, want reason MachineStateEphemeral and no staging", conv)
	}
}

func TestCredentialsStagingIsIdempotent(t *testing.T) {
	desired := desiredCredentials()
	_, hash, _ := machine.RenderRegistryCredentials(desired)
	conv := decideRegistriesConvergence(registriesInputs{desired: desired}, machineWithPolicy(machine.RebootAuto),
		credentialFacts(""), nil, hash, turnGranted)
	if conv.stage {
		t.Error("the convergence stages a document whose hash is already staged, want no write")
	}
}
