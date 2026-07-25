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
	tree  machine.FactsTree

	// restarts counts the in-place k3s restarts this boot performed. The
	// restart path is the only writer of boot/restarts, so it holds the
	// count itself and starts it at zero: a fresh boot has done none.
	restarts int

	// What k3s runs now: the choices from the boot, updated by each
	// applied restart.
	clusterDoc  *cluster.Cluster
	clusterRaw  []byte
	creds       *machine.RegistryCredentials
	credsSource machine.ManifestSource

	// The seeded files of retracted janitor-teardown features, queued
	// by retractFeatureManifests for removal in the window where k3s
	// is down (removeOfflineRetractions).
	offlineRetractions []string

	writeBootConfig  func(*cluster.Cluster, *machine.Machine, []*connection) (api.Role, error)
	actuateFeatures  func(*cluster.Cluster, string) []machine.FeatureStatus
	renderRegistries func(*cluster.Cluster, *machine.RegistryCredentials, machine.ManifestStore, machine.ManifestSource) machine.RegistriesStatus
}

// newRestartState creates a restartState with the real
// implementations. Tests build the struct directly, with seams of
// their own.
func newRestartState(root string, m *machine.Machine, conns []*connection, tree machine.FactsTree,
	clusterDoc *cluster.Cluster, clusterRaw []byte, creds *machine.RegistryCredentials,
	credsSource machine.ManifestSource) *restartState {
	return &restartState{
		root: root, m: m, conns: conns, tree: tree,
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

	// The facts update before the restart. They name the staged
	// documents, so the operator knows what this restart applied. The
	// write order is a commit protocol: features/ and registries/ land
	// first, then the restart counter, and boot/clusterManifest lands
	// last, because the operator's promotion keys on that record
	// (machine-operator/cluster.go). The boot cluster manifest
	// publication carries the exact bytes the operator compares against.
	s.restarts++
	s.tree.WriteFeatures(featureStatuses)
	s.tree.WriteRegistries(registries)
	s.tree.WriteRuntime(runtimeFacts(clusterDoc, machineMemoryBytes()))
	if stagedCreds != nil {
		s.tree.WriteBootCredentials(machine.ManifestSourceStaged, machine.ManifestHash(stagedCredsRaw))
	}
	s.tree.WriteBootRestarts(s.restarts)
	if applyingCluster {
		s.tree.WriteBootClusterManifest(machine.ManifestSourceStaged, clusterHash)
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
// that the new document no longer declares, so that nothing seeds
// them again. Removing a manifest is not a deletion. k3s's deploy
// controller walks the files that are there and applies them, and
// nothing reconciles an addon against a source file that has gone,
// so the addon and every object it created stay in the cluster. The
// cluster operator's janitor is what deletes a retracted feature's
// workloads, on this path and on the boot path alike
// (cluster-operator/janitor.go).
//
// A janitor-teardown feature's files are queued rather than removed,
// and removeOfflineRetractions removes them once k3s has stopped.
// Given the paragraph above, that ordering guards against nothing
// k3s does: no pass acts on a removed file whether k3s is up or
// down. The queue stays because it costs one list, and the failure
// it rules out reaches the whole fleet. If a deletion did cascade
// from the sync objects while the flux controllers still ran, the
// engine's own deletion finalizer would prune everything the
// repository ever applied, the fleet's own documents included. The
// janitor tears flux down in an order that stops the controllers
// first, and the queue keeps the file removal outside that order.
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
		var files []string
		for _, manifest := range manifests {
			files = append(files, filepath.Base(manifest))
		}
		files = append(files, renderedFeatureManifests[slug]...)

		def := cluster.FeatureBySlug(slug)
		if def != nil && def.Teardown == cluster.TeardownJanitor {
			for _, file := range files {
				s.offlineRetractions = append(s.offlineRetractions, filepath.Join(k3sManifestsDir, file))
			}
			continue
		}
		for _, file := range files {
			if err := os.Remove(filepath.Join(k3sManifestsDir, file)); err == nil {
				fmt.Printf("liken: restart: retracted %s; the janitor deletes its workload\n", file)
			}
		}
	}
}

// removeOfflineRetractions removes the files that
// retractFeatureManifests queued, in the window where k3s is down.
// The supervisor calls it right after each restart's stop. The files
// go while k3s is stopped, so no addon pass runs against the
// removal; the cluster operator's janitor owns that teardown.
func (s *restartState) removeOfflineRetractions() {
	for _, path := range s.offlineRetractions {
		if err := os.Remove(path); err == nil {
			fmt.Printf("liken: restart: retracted %s while k3s is down; the janitor tears its objects down\n", filepath.Base(path))
		}
	}
	s.offlineRetractions = nil
}
