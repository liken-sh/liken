package main

// The restart path applies staged restart-class changes without a
// reboot.
//
// A reboot applies staged documents by re-running the whole boot
// process. The restart path does the same work, but only for the
// parts k3s reads at process start: the boot drop-in, registries.yaml,
// and the feature actuation. Init re-renders these from the staged
// documents while k3s still runs. Only after that does init restart
// the child process (see supervisor.go). Downtime is therefore one
// graceful stop and start, and every container stays running under
// its shim.
//
// The staged stores decide what to apply. The intent file only
// signals that new work exists. So a duplicate intent is harmless:
// if a pass finds nothing new to apply, it returns false and does
// not disturb k3s. Both the boot path and the restart path use the
// same classifier (see cluster/changes.go). If a staged document's
// changes need a reboot, the restart path leaves it staged for the
// reboot path. It never applies part of that document here.
//
// Promotion needs no extra step. The proof that a cluster document
// works has always been the operator seeing the machine serve
// correctly under it. The restart path writes the attempted marker
// and publishes new facts that name the staged document. This is the
// same state a proving boot leaves. The operator's next check
// promotes the document. If k3s does not come back, the next real
// boot finds the attempted marker matching the staged document and
// rejects it with a fallback. This is the one-trial rule, and it
// applies the same way here. Credentials promote at actuation time,
// the same as at boot (see registries.go).

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// restartState holds everything the restart path needs. main gathers
// most of this during the boot. The struct also holds the current
// documents, which each successful apply updates. The function
// fields are seams for tests: tests use them to check the decisions
// and file effects, without a kernel to load modules into and
// without a network to get addresses from.
type restartState struct {
	root  string
	m     *machine.Machine
	conns []*connection
	facts *factsFile

	// What k3s runs now: the choices from the boot, updated by each
	// applied restart.
	clusterDoc  *cluster.Cluster
	clusterRaw  []byte
	creds       *machine.RegistryCredentials
	credsSource machine.ManifestSource

	writeBootConfig  func(*cluster.Cluster, *machine.Machine, []*connection) (api.Role, error)
	actuateFeatures  func(*cluster.Cluster, string) []machine.FeatureStatus
	renderRegistries func(*cluster.Cluster, *machine.RegistryCredentials, machine.ManifestStore, machine.ManifestSource) machine.RegistriesStatus
}

// newRestartState creates a restartState with the real
// implementations. Tests build the struct directly, with seams of
// their own.
func newRestartState(root string, m *machine.Machine, conns []*connection, facts *factsFile,
	clusterDoc *cluster.Cluster, clusterRaw []byte, creds *machine.RegistryCredentials,
	credsSource machine.ManifestSource) *restartState {
	return &restartState{
		root: root, m: m, conns: conns, facts: facts,
		clusterDoc: clusterDoc, clusterRaw: clusterRaw, creds: creds, credsSource: credsSource,
		writeBootConfig:  writeK3sBootConfig,
		actuateFeatures:  actuateFeatures,
		renderRegistries: writeRegistriesConfig,
	}
}

// apply is the supervisor's applyRestart callback. It loads whatever
// is staged, checks it, runs the restart-class rendering, and
// reports whether the restart is worth doing. Everything here runs
// while k3s still serves.
func (s *restartState) apply(intent machine.RestartIntent) bool {
	fmt.Printf("liken: restart requested: %s\n", intent.Reason)

	stagedCluster, stagedRaw, clusterHash := s.stagedClusterDocument()
	stagedCreds, stagedCredsRaw := s.stagedCredentials()
	if stagedCluster == nil && stagedCreds == nil {
		fmt.Println("liken: restart: nothing staged that a restart could apply; k3s keeps running")
		return false
	}

	// The cluster document part: re-render the boot drop-in and
	// re-run feature actuation under the staged document.
	clusterDoc, clusterRaw := s.clusterDoc, s.clusterRaw
	applyingCluster := stagedCluster != nil
	if applyingCluster {
		if _, err := s.writeBootConfig(stagedCluster, s.m, s.conns); err != nil {
			// A document that fails to render would also fail the
			// next boot. Quarantine it now, and keep serving the
			// current document.
			rejectStagedDocument("cluster", "document", machine.ClusterManifests(s.root).Reject,
				stagedRaw, fmt.Sprintf("the staged cluster document does not render a k3s configuration: %v", err))
			applyingCluster = false
		} else {
			if err := machine.ClusterManifests(s.root).WriteAttempted(clusterHash); err != nil {
				fmt.Fprintf(os.Stderr, "liken: restart: marking the staged document attempted: %v\n", err)
			}
			clusterDoc, clusterRaw = stagedCluster, stagedRaw
		}
	}

	// The credentials part runs whether or not the cluster document
	// changed. writeRegistriesConfig promotes staged credentials
	// after it writes the file.
	creds, credsSource := s.creds, s.credsSource
	if stagedCreds != nil {
		creds, credsSource = stagedCreds, machine.ManifestSourceStaged
	}
	if !applyingCluster && stagedCreds == nil {
		return false
	}

	featureStatuses := s.actuateFeatures(clusterDoc, s.m.Metadata.Name)
	if applyingCluster {
		s.retractFeatureManifests(s.clusterDoc, clusterDoc)
	}
	registries := s.renderRegistries(clusterDoc, creds, machine.RegistryCredentialsStore(s.root), credsSource)

	// The facts update before the restart. The facts name the staged
	// documents. The cluster document's entry tells the operator when
	// to promote it. The boot cluster manifest publication carries
	// the bytes the operator compares against. The restart counter
	// records that this change happened without a boot.
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

	// The applied documents are now current. A duplicate intent finds
	// nothing staged, because credentials are promoted, or finds an
	// attempted marker that matches staged, because the operator has
	// not yet promoted the cluster document. Either way, the
	// duplicate intent applies nothing.
	s.clusterDoc, s.clusterRaw = clusterDoc, clusterRaw
	s.creds, s.credsSource = creds, credsSource
	return true
}

// stagedClusterDocument loads and checks the staged cluster document.
// It returns nil when there is nothing for a restart to apply:
//
//   - There is no staged file.
//   - A document was already attempted, by this restart or by a
//     previous boot. The operator's promotion, or the next boot's
//     rejection, will settle it.
//   - A document fails to parse. This function quarantines it.
//   - A document's changes are reboot-class. This function leaves it
//     staged for the reboot path, because the operator asked for a
//     reboot. This check also stops a racing restart intent from
//     applying part of the document.
func (s *restartState) stagedClusterDocument() (*cluster.Cluster, []byte, string) {
	store := machine.ClusterManifests(s.root)
	raw, err := store.LoadStaged()
	if err != nil || raw == nil {
		return nil, nil, ""
	}
	hash := machine.ManifestHash(raw)
	if attempted, _ := store.LoadAttempted(); attempted == hash {
		return nil, nil, ""
	}
	staged, perr := cluster.ParseCluster(raw)
	if perr != nil {
		rejectStagedDocument("cluster", "document", store.Reject,
			raw, fmt.Sprintf("the staged cluster document does not parse: %v", perr))
		return nil, nil, ""
	}
	if s.clusterDoc == nil {
		return nil, nil, ""
	}
	if !cluster.RestartApplies(s.clusterDoc.Spec, staged.Spec) {
		fmt.Println("liken: restart: the staged cluster document needs a reboot; leaving it for one")
		return nil, nil, ""
	}
	return staged, raw, hash
}

// stagedCredentials loads and checks the staged credentials document.
// It returns nil when nothing is staged. Credentials promote at
// actuation, so a staged file always means unapplied work. If a
// document fails to parse, this function quarantines it, the same as
// at boot.
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
// that the new document no longer declares. k3s still runs and
// watches its auto-deploy directory, so it detects each removal and
// deletes the addon itself. This is better than the boot path, where
// the file disappears while k3s is down, and the cluster operator's
// janitor must clean up after it. The janitor still handles exactly
// that boot path.
func (s *restartState) retractFeatureManifests(old, new *cluster.Cluster) {
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
