package main

// The restart path applies staged restart-class changes without a
// reboot.
//
// A reboot applies staged documents by running the whole boot again.
// The restart path redoes only what k3s reads when its process
// starts: the boot drop-in, registries.yaml, and feature actuation.
// Init renders these from the staged documents while k3s still runs,
// and then the supervisor restarts the k3s process (supervisor.go).
// k3s is down for one graceful stop and start, and every container
// keeps running under its shim.
//
// A restart intent only signals that something may be staged. What
// apply applies comes from the staged stores, so a duplicate intent
// finds nothing new, apply returns false, and k3s keeps running. The
// restart path
// and the boot path classify changes with the same code
// (cluster/changes.go). A staged document that needs a reboot stays
// staged for the boot path, and the restart path applies none of it.
//
// A restart does not promote the cluster document. The operator
// promotes it when it reads the document that the restart published.
// Until then the restart path leaves what a trial boot leaves: the
// attempted marker, and facts that name the staged document. If the
// machine reboots before the operator promotes it, the next boot finds
// the attempted marker and rejects the document. The operator reads the
// published document as soon as init writes it, which is before k3s
// starts again under it, so in practice the promotion comes first and a
// k3s that does not start again under the document is not caught by
// the marker (milestone 103). Credentials are promoted when they are
// applied, as at boot (registries.go).

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// restartState is filled by main during boot. Tests replace the
// function fields, so they run without a kernel to load modules into
// or a network to get addresses from.
type restartState struct {
	root  string
	m     *machine.Machine
	conns []*connection
	tree  machine.FactsTree

	// restarts counts the k3s restarts since boot, for the
	// boot/restarts fact. It is in memory, so each boot starts at zero.
	restarts int

	// The documents k3s runs under now. Boot sets them, and each
	// applied restart replaces them.
	clusterDoc  *cluster.Cluster
	clusterRaw  []byte
	creds       *machine.RegistryCredentials
	credsSource machine.ManifestSource

	// Paths of manifest files of retracted TeardownJanitor features,
	// which removeQueuedRetractions deletes while k3s is stopped.
	queuedRetractions []string

	writeBootConfig  func(*cluster.Cluster, *machine.Machine, []*connection) (api.Role, error)
	actuateFeatures  func(*cluster.Cluster, string) []machine.FeatureStatus
	renderRegistries func(*cluster.Cluster, *machine.RegistryCredentials, machine.ManifestStore, machine.ManifestSource) machine.RegistriesStatus
}

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

// apply is the supervisor's applyRestart callback. It loads and
// checks the staged documents and renders their restart-class parts
// while k3s is still running. It returns whether k3s needs to restart.
func (s *restartState) apply(intent machine.RestartIntent) bool {
	fmt.Printf("liken: restart requested: %s\n", intent.Reason)

	stagedCluster, stagedRaw, clusterHash := s.stagedClusterDocument()
	stagedCreds, stagedCredsRaw := s.stagedCredentials()
	if stagedCluster == nil && stagedCreds == nil {
		fmt.Println("liken: restart: the staged documents hold nothing that a restart applies, so k3s keeps running")
		return false
	}

	// clusterDoc is the document that feature actuation and the
	// registries render under: the staged one when its boot drop-in
	// renders, the current one otherwise.
	clusterDoc, clusterRaw := s.clusterDoc, s.clusterRaw
	applyingCluster := stagedCluster != nil
	if applyingCluster {
		if _, err := s.writeBootConfig(stagedCluster, s.m, s.conns); err != nil {
			// The next boot would fail to render this document too, so
			// reject it now and keep running the current one.
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

	// Credentials apply even when the cluster document does not.
	// writeRegistriesConfig promotes staged credentials after it
	// writes the file.
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

	// The operator promotes the staged cluster document when the
	// document that publishBootClusterManifest writes has its bytes
	// (machine-operator/cluster.go). The operator watches the facts
	// tree and not that file, so boot/clusterManifest is written after
	// it, as the last write. The wake that the record sends then starts
	// a pass that reads the published document, and the other facts
	// already describe the document.
	s.restarts++
	s.tree.WriteFeatures(featureStatuses)
	s.tree.WriteRegistries(registries)
	s.tree.WriteRuntime(runtimeFacts(clusterDoc, clusterDoc.Role(s.m.Metadata.Name), machineMemoryBytes()))
	if stagedCreds != nil {
		s.tree.WriteBootCredentials(machine.ManifestSourceStaged, machine.ManifestHash(stagedCredsRaw))
	}
	s.tree.WriteBootRestarts(s.restarts)
	if applyingCluster {
		publishBootClusterManifest(clusterRaw)
		s.tree.WriteBootClusterManifest(machine.ManifestSourceStaged, clusterHash)
	}

	// After this, a duplicate intent applies nothing: the applied
	// credentials are promoted, and the cluster document, which stays
	// staged until the operator promotes it, matches its attempted
	// marker.
	s.clusterDoc, s.clusterRaw = clusterDoc, clusterRaw
	s.creds, s.credsSource = creds, credsSource
	return true
}

// stagedClusterDocument loads and checks the staged cluster document.
// It returns nil when a restart has nothing to apply:
//
//   - No document is staged.
//   - The staged document was already attempted, by an earlier
//     restart or boot. The operator promotes it, or the next boot
//     rejects it.
//   - The document does not parse. This function rejects it.
//   - There is no current document to compare it with.
//   - The document has changes that need a reboot. It stays staged
//     for the boot path, so a restart intent that arrives beside the
//     reboot request does not apply part of it.
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
		fmt.Println("liken: restart: the staged cluster document needs a reboot to apply, so it stays staged")
		return nil, nil, ""
	}
	return staged, raw, hash
}

// stagedCredentials loads the staged credentials document. It returns
// nil when none is staged, and it rejects a document that does not
// parse, as the boot path does. Applying credentials promotes them, so
// a staged document has never been applied.
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

// retractFeatureManifests deletes, from k3s's auto-deploy directory,
// the manifest files of each feature that old enables and new does
// not. Deleting a file does not delete what k3s deployed from it,
// because k3s's deploy controller applies the files that exist and
// does not reconcile an Addon whose file is gone. The cluster
// operator's janitor deletes a retracted feature's workloads
// (cluster-operator/janitor.go).
//
// For a TeardownJanitor feature, the files are queued instead, and
// removeQueuedRetractions deletes them after k3s stops. This is a
// precaution for flux. If k3s ever deleted flux's sync objects while
// the flux controllers ran, flux's finalizer would prune everything
// the repository applied, the fleet's own documents included. The
// janitor stops the controllers before it deletes flux's objects.
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
				s.queuedRetractions = append(s.queuedRetractions, filepath.Join(k3sManifestsDir, file))
			}
			continue
		}
		for _, file := range files {
			if err := os.Remove(filepath.Join(k3sManifestsDir, file)); err == nil {
				fmt.Printf("liken: restart: removed %s, and the janitor deletes its workload\n", file)
			}
		}
	}
}

// removeQueuedRetractions deletes the files that
// retractFeatureManifests queued. The supervisor calls it after it
// stops k3s for a restart and before it starts k3s again.
func (s *restartState) removeQueuedRetractions() {
	for _, path := range s.queuedRetractions {
		if err := os.Remove(path); err == nil {
			fmt.Printf("liken: restart: removed %s while k3s is stopped, and the janitor deletes its objects\n", filepath.Base(path))
		}
	}
	s.queuedRetractions = nil
}
