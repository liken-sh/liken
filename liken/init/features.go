package main

// Init turns on the features that the Cluster enables in spec.features,
// and returns one status for each of them for status.features.
//
// liken/cluster/features.go gives each feature its kind. init only
// reports a Bundled or Embedded feature Active, because k3s.go turns
// it on when it renders the k3s drop-in: a Bundled feature is left off
// the disable list, and an Embedded feature's own disable key is left
// out. A Vendored
// feature ships in the image and is unused until a Cluster enables
// it: kernel modules listed in /etc/liken/features/<slug>/modules.conf,
// and sometimes manifests and a file that init writes for this
// machine. A Workload feature is a set of manifests that k3s applies.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// Tests point these at temporary directories.
var (
	featuresDir = "/etc/liken/features"
	// liken writes its manifests into a subdirectory of k3s's
	// auto-deploy directory, because k3s writes the manifests of its
	// own components at the top, and reads a disabled component's file
	// there at startup to remove that component. k3s applies the whole
	// tree and names each addon after its file name, so the
	// subdirectory does not change the addon names.
	k3sManifestsDir = "/var/lib/rancher/" + likenManifestsRel
	iscsiDir        = "/etc/iscsi"
)

func actuateFeatures(clusterDoc *cluster.Cluster, machineName string) []machine.FeatureStatus {
	slugs := clusterDoc.EnabledFeatures()
	if len(slugs) == 0 {
		return nil
	}
	moduleBase := filepath.Join("/lib/modules", kernelRelease())
	statuses := make([]machine.FeatureStatus, 0, len(slugs))
	for _, slug := range slugs {
		var status machine.FeatureStatus
		def := cluster.FeatureBySlug(slug)
		var paramsErr error
		if def != nil {
			paramsErr = def.ValidateParams(clusterDoc.Spec.Features[slug])
		}
		switch {
		case def == nil:
			// The parser accepts a slug that this image does not
			// define, so that a machine on an older release than the
			// Cluster still boots. The feature is reported Missing
			// here instead.
			status = machine.FeatureStatus{
				Name:  slug,
				State: machine.FeatureMissing,
				Message: fmt.Sprintf(
					"this image has no %q feature. Upgrade to a release that has it, or correct the name if it is misspelled (this image has: %s)",
					slug, strings.Join(cluster.FeatureSlugs(), ", ")),
			}
		case paramsErr != nil:
			// The parser also accepts a parameter that this image does
			// not define, for the same reason. The whole feature fails,
			// so that the machine does not run a part of what the
			// Cluster asked for.
			status = machine.FeatureStatus{
				Name:    slug,
				State:   machine.FeatureFailed,
				Message: paramsErr.Error(),
			}
		case def.Kind == cluster.FeatureVendored:
			status = actuateVendoredFeature(moduleBase, slug, machineName)
		case def.Kind == cluster.FeatureWorkload:
			status = actuateWorkloadFeature(clusterDoc, slug)
		default:
			status = machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
		}
		if status.Message != "" {
			fmt.Printf("liken: features: %s: %s: %s\n",
				status.Name, strings.ToLower(string(status.State)), status.Message)
		} else {
			fmt.Printf("liken: features: %s: %s\n",
				status.Name, strings.ToLower(string(status.State)))
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// actuateVendoredFeature loads a vendored feature's kernel modules,
// runs its boot hook, and copies its manifests for k3s. A failed step
// returns a Missing or Failed status and does not stop the boot,
// because a machine without the feature can still serve the rest of
// the cluster.
func actuateVendoredFeature(moduleBase, slug, machineName string) machine.FeatureStatus {
	status := machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
	dir := filepath.Join(featuresDir, slug)

	// Every image that has a vendored feature has its modules.conf, so
	// a missing file means that the image is older than the feature.
	modulesConf := filepath.Join(dir, "modules.conf")
	if _, err := os.Stat(modulesConf); errors.Is(err, fs.ErrNotExist) {
		status.State = machine.FeatureMissing
		status.Message = fmt.Sprintf(
			"this image does not have the %s feature. Upgrade to a release that has it", slug)
		return status
	}

	// The feature fails when any one module does not load, because a
	// storage client without its transport module does not work.
	names, err := readModuleList(modulesConf)
	if err != nil {
		status.State = machine.FeatureFailed
		status.Message = err.Error()
		return status
	}
	for _, m := range loadDeclaredModulesFrom(moduleBase, names, nil) {
		if m.State == machine.ModuleLoaded || m.State == machine.ModuleBuiltin {
			continue
		}
		status.State = machine.FeatureFailed
		status.Message = fmt.Sprintf("module %s: %s", m.Name, m.Message)
		return status
	}

	if hook := featureBootHooks[slug]; hook != nil {
		if err := hook(machineName); err != nil {
			status.State = machine.FeatureFailed
			status.Message = err.Error()
			return status
		}
	}

	if err := seedFeatureManifests(slug); err != nil {
		status.State = machine.FeatureFailed
		status.Message = err.Error()
		return status
	}
	return status
}

// actuateWorkloadFeature copies a workload feature's manifests for
// k3s. An image that defines the slug always has its manifests, so
// there is no modules.conf check. For flux it first renders the sync
// objects from the parameters, so a Cluster with a missing parameter
// reports Failed instead of starting Flux with nothing to sync.
func actuateWorkloadFeature(clusterDoc *cluster.Cluster, slug string) machine.FeatureStatus {
	status := machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
	if slug == cluster.FeatureFlux {
		cfg, err := clusterDoc.FluxConfig()
		if err == nil {
			err = seedFluxSync(cfg)
		}
		if err != nil {
			status.State = machine.FeatureFailed
			status.Message = err.Error()
			return status
		}
	}
	if err := seedFeatureManifests(slug); err != nil {
		status.State = machine.FeatureFailed
		status.Message = err.Error()
		return status
	}
	return status
}

// seedFluxSync writes the flux-system GitRepository and Kustomization
// that `flux bootstrap` would commit to the repository. init writes
// them from the Cluster on every boot, so the Cluster is the place to
// change them, and a copy in the repository and this file would
// overwrite each other. A repository that already has its own
// flux-system Kustomization must set prune to "false". Otherwise the
// Kustomization lists itself in its own inventory, and the first
// build that stops producing it deletes everything that the
// repository applied. The Flux controllers are the other way around:
// liken/cluster-operator/flux.go creates them once, and the
// repository owns them after that.
func seedFluxSync(cfg *cluster.FluxConfig) error {
	// Flux writes sync paths relative to the repository root with a
	// "./" prefix.
	path := "./"
	if cfg.Path != "." {
		path = "./" + strings.TrimPrefix(cfg.Path, "./")
	}
	// %q writes a JSON string, which is a valid YAML scalar, so YAML
	// syntax in a URL, branch, or path cannot change the document.
	rendered := fmt.Sprintf(`# Rendered by liken from the Cluster document's flux declaration.
# liken re-renders these two objects on every boot, so a copy in git
# would fight this one. A repository that carries its own copies must
# declare prune: "false" in the Cluster document.
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: flux-system
  namespace: flux-system
spec:
  interval: 1m0s
  url: %q
  ref:
    branch: %q
  secretRef:
    name: flux-system
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: flux-system
  namespace: flux-system
spec:
  interval: 10m0s
  path: %q
  prune: %t
  sourceRef:
    kind: GitRepository
    name: flux-system
`, cfg.Repository, cfg.Branch, path, cfg.Prune)
	if err := os.MkdirAll(k3sManifestsDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(k3sManifestsDir, "flux-sync.yaml"), []byte(rendered), 0o644)
}

// featureBootHooks maps a feature's slug to a function that writes
// the files that the feature needs for this machine at boot.
var featureBootHooks = map[string]func(machineName string) error{
	"iscsi": writeInitiatorName,
}

// writeInitiatorName writes the iSCSI initiator name that this
// machine sends when it logs in to a target, and that storage arrays
// put in their access lists. It is made from the machine name, so a
// reinstalled machine gets the same name with nothing stored. The
// prefix is an iSCSI qualified name (RFC 3720): "iqn.", a year and
// month when the owner held the domain, then the domain liken.sh
// reversed. The host's iscsiadm, which CSI drivers run through a
// chroot, reads the file, and so does the iscsid DaemonSet through
// its hostPath mount of /etc/iscsi.
func writeInitiatorName(machineName string) error {
	if err := os.MkdirAll(iscsiDir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("InitiatorName=iqn.2026-07.sh.liken:%s\n", machineName)
	return os.WriteFile(filepath.Join(iscsiDir, "initiatorname.iscsi"), []byte(name), 0o600)
}

// renderedFeatureManifests lists the files that init renders for a
// feature in addition to the image's manifests. The code that removes
// a feature's manifests reads it, because these files are not in the
// image.
var renderedFeatureManifests = map[string][]string{
	cluster.FeatureFlux: {"flux-sync.yaml"},
}

// featureManifestPaths returns the paths of a feature's manifests in
// the image. The result is empty for a feature with no workload.
func featureManifestPaths(slug string) ([]string, error) {
	return filepath.Glob(filepath.Join(featuresDir, slug, "manifests", "*.yaml"))
}

// seedFeatureManifests copies a feature's manifests into liken's
// auto-deploy subdirectory. It never removes one. At boot,
// seedClusterState has already reset the subdirectory to the image's
// own manifests, and on the restart path retractFeatureManifests
// removes those of a feature that the Cluster no longer enables. Every
// machine gets the copies, although only a leader's k3s reads them, so
// a machine that becomes a leader already has them.
func seedFeatureManifests(slug string) error {
	manifests, err := featureManifestPaths(slug)
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		raw, err := os.ReadFile(manifest)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(k3sManifestsDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(k3sManifestsDir, filepath.Base(manifest)), raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}
