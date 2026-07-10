package main

// The restart path: applying staged restart-class changes without a
// reboot.
//
// A reboot applies staged documents by re-running the whole boot.
// The restart tier is that same actuation, narrowed to what k3s
// reads only at process start: the boot drop-in, registries.yaml,
// and the feature actuation. Init re-renders all of it from the
// staged documents while k3s still serves, and only then bounces
// the child (supervisor.go), so downtime is one graceful stop and
// start with every container still running under its shim.
//
// The staged stores are the authority on what to apply; the intent
// file only announces that there is work. Duplicate intents are
// therefore harmless: a pass that finds nothing new to apply answers
// false and k3s is not disturbed. And both programs consult the same
// classifier (machine/changes.go): a staged document whose changes
// need a reboot is left standing for the reboot path, never
// half-applied here.
//
// Promotion needs nothing new. The cluster document's proof was
// always the operator observing the machine serving under it: the
// restart path writes the attempted marker and republishes the
// facts naming the staged document, exactly the state a proving
// boot leaves, and the operator's next pass promotes it (or, if
// k3s never comes back, the next real boot finds attempted matching
// staged and rejects with fallback — the one-trial rule, unchanged).
// Credentials promote at actuation, as at boot (registries.go).

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisguidry/liken/machine"
)

// restartState is everything the restart path needs that main
// gathered during the boot, plus the current documents, which each
// successful apply advances. The function fields are seams: tests
// exercise the decision and file effects without a kernel to load
// modules into or a network to derive addresses from.
type restartState struct {
	root  string
	m     *machine.Machine
	conns []*connection
	facts *factsFile

	// What k3s currently runs: the boot's choices, advanced by each
	// applied restart.
	cluster     *machine.Cluster
	clusterRaw  []byte
	creds       *machine.RegistryCredentials
	credsSource machine.ManifestSource

	writeBootConfig  func(*machine.Cluster, *machine.Machine, []*connection) (machine.Role, error)
	actuateFeatures  func(*machine.Cluster, string) []machine.FeatureStatus
	renderRegistries func(*machine.Cluster, *machine.RegistryCredentials, machine.ManifestStore, machine.ManifestSource) machine.RegistriesStatus
}

// newRestartState wires the real implementations; tests build the
// struct directly with seams of their own.
func newRestartState(root string, m *machine.Machine, conns []*connection, facts *factsFile,
	cluster *machine.Cluster, clusterRaw []byte, creds *machine.RegistryCredentials,
	credsSource machine.ManifestSource) *restartState {
	return &restartState{
		root: root, m: m, conns: conns, facts: facts,
		cluster: cluster, clusterRaw: clusterRaw, creds: creds, credsSource: credsSource,
		writeBootConfig:  writeK3sBootConfig,
		actuateFeatures:  actuateFeatures,
		renderRegistries: writeRegistriesConfig,
	}
}

// apply is the supervisor's applyRestart callback: load whatever is
// staged, vet it, actuate the restart-class rendering, and answer
// whether the bounce is worth taking. Everything here runs while
// k3s still serves.
func (s *restartState) apply(intent machine.RestartIntent) bool {
	fmt.Printf("liken: restart requested: %s\n", intent.Reason)

	stagedCluster, stagedRaw, clusterHash := s.stagedClusterDocument()
	stagedCreds, stagedCredsRaw := s.stagedCredentials()
	if stagedCluster == nil && stagedCreds == nil {
		fmt.Println("liken: restart: nothing staged that a restart could apply; k3s keeps running")
		return false
	}

	// The cluster document half: re-render the boot drop-in and
	// re-run feature actuation under the staged document.
	cluster, clusterRaw := s.cluster, s.clusterRaw
	applyingCluster := stagedCluster != nil
	if applyingCluster {
		if _, err := s.writeBootConfig(stagedCluster, s.m, s.conns); err != nil {
			// A document that won't render would fail the next boot
			// identically: quarantine it now, keep serving the
			// current one.
			rejectStagedDocument("cluster", "document", machine.ClusterManifests(s.root).Reject,
				stagedRaw, fmt.Sprintf("the staged cluster document does not render a k3s configuration: %v", err))
			applyingCluster = false
		} else {
			if err := machine.ClusterManifests(s.root).WriteAttempted(clusterHash); err != nil {
				fmt.Fprintf(os.Stderr, "liken: restart: marking the staged document attempted: %v\n", err)
			}
			cluster, clusterRaw = stagedCluster, stagedRaw
		}
	}

	// The credentials half rides along whether or not the cluster
	// document changed; writeRegistriesConfig promotes staged
	// credentials once the file is written.
	creds, credsSource := s.creds, s.credsSource
	if stagedCreds != nil {
		creds, credsSource = stagedCreds, machine.ManifestSourceStaged
	}
	if !applyingCluster && stagedCreds == nil {
		return false
	}

	featureStatuses := s.actuateFeatures(cluster, s.m.Metadata.Name)
	if applyingCluster {
		s.retractFeatureManifests(s.cluster, cluster)
	}
	registries := s.renderRegistries(cluster, creds, machine.RegistryCredentialsStore(s.root), credsSource)

	// The facts update before the bounce: they name the staged
	// documents (the cluster document's entry is the operator's
	// promotion cue), the boot cluster manifest publication carries
	// the bytes the operator diffs against, and the restart counter
	// records that this change arrived without a boot.
	s.facts.publish(func(status *machine.MachineStatus) {
		if applyingCluster {
			status.Boot.ClusterManifestSource = machine.ManifestSourceStaged
			status.Boot.ClusterManifestHash = clusterHash
		}
		if stagedCreds != nil {
			status.Boot.CredentialsSource = machine.ManifestSourceStaged
			status.Boot.CredentialsHash = machine.ManifestHash(stagedCredsRaw)
		}
		status.Boot.Restarts++
		status.Features = featureStatuses
		status.Registries = registries
	})
	if applyingCluster {
		publishBootClusterManifest(clusterRaw)
	}

	// The applied documents are now current: a duplicate intent
	// finds nothing staged (credentials were promoted) or an
	// attempted marker matching staged (the cluster document, until
	// the operator promotes it) and applies nothing.
	s.cluster, s.clusterRaw = cluster, clusterRaw
	s.creds, s.credsSource = creds, credsSource
	return true
}

// stagedClusterDocument loads and vets the staged cluster document,
// answering nil when there is nothing for a restart to apply: no
// staged file, a document already attempted (this restart or a
// previous boot tried it; the operator's promotion or the next
// boot's rejection settles it), a document that won't parse
// (quarantined here), or one whose changes are reboot-class (left
// standing for the reboot path — the operator asked for a reboot in
// that case, and this guard is what keeps a racing restart intent
// from half-applying it).
func (s *restartState) stagedClusterDocument() (*machine.Cluster, []byte, string) {
	store := machine.ClusterManifests(s.root)
	raw, err := store.LoadStaged()
	if err != nil || raw == nil {
		return nil, nil, ""
	}
	hash := machine.ManifestHash(raw)
	if attempted, _ := store.LoadAttempted(); attempted == hash {
		return nil, nil, ""
	}
	staged, perr := machine.ParseCluster(raw)
	if perr != nil {
		rejectStagedDocument("cluster", "document", store.Reject,
			raw, fmt.Sprintf("the staged cluster document does not parse: %v", perr))
		return nil, nil, ""
	}
	if s.cluster == nil {
		return nil, nil, ""
	}
	if !machine.RestartApplies(s.cluster.Spec, staged.Spec) {
		fmt.Println("liken: restart: the staged cluster document needs a reboot; leaving it for one")
		return nil, nil, ""
	}
	return staged, raw, hash
}

// stagedCredentials loads and vets the staged credentials document,
// nil when nothing is staged (credentials promote at actuation, so
// a staged file always means unapplied work). A document that won't
// parse is quarantined, exactly as at boot.
func (s *restartState) stagedCredentials() (*machine.RegistryCredentials, []byte) {
	store := machine.RegistryCredentialsStore(s.root)
	raw, err := store.LoadStaged()
	if err != nil || raw == nil {
		return nil, nil
	}
	creds, perr := machine.ParseRegistryCredentials(raw)
	if perr != nil {
		rejectStagedDocument("registries", "credentials", store.Reject,
			raw, fmt.Sprintf("the staged credentials document does not parse: %v", perr))
		return nil, nil
	}
	return creds, raw
}

// retractFeatureManifests removes the seeded manifests of features
// the new document no longer declares. k3s is still running and
// watches its auto-deploy directory, so it witnesses each removal
// and deletes the addon natively — better than the boot path, where
// the file vanishes while k3s is down and the cluster operator's
// janitor must clean up after it. The janitor stays for exactly
// that boot path.
func (s *restartState) retractFeatureManifests(old, new *machine.Cluster) {
	declared := map[string]bool{}
	for _, slug := range new.EnabledFeatures() {
		declared[slug] = true
	}
	for _, slug := range old.EnabledFeatures() {
		if declared[slug] {
			continue
		}
		manifests, err := featureManifestPaths(slug)
		if err != nil {
			continue
		}
		for _, manifest := range manifests {
			seeded := filepath.Join(k3sManifestsDir, filepath.Base(manifest))
			if err := os.Remove(seeded); err == nil {
				fmt.Printf("liken: restart: retracted %s; k3s deletes its workload\n", filepath.Base(manifest))
			}
		}
	}
}
