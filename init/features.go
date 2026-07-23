package main

// Actuating the cluster's opt-in features on this machine.
//
// The Cluster document's spec.features lists the fleet's opt-ins from
// liken's curated vocabulary (the cluster package's features.go).
// This pass is init's half of carrying them out: one verdict per
// enabled feature, bound for status.features through the facts file,
// with a console line for each, so the console and the API tell the
// same story. The code validates the cluster document against the
// vocabulary at parse time, so every slug that reaches this pass is a
// known one.
//
// Two kinds of feature arrive here, distinguished by the vocabulary
// table and never by the user. A bundled feature is a component the
// k3s binary already carries. Its whole actuation is the disable
// list this boot renders into the k3s drop-in (k3s.go); nothing
// about it can be missing from the image, and it always reports
// Active. A vendored feature is a payload the image ships inert:
// kernel modules listed at /etc/liken/features/<slug>/modules.conf,
// sometimes a workload manifest alongside them, sometimes a
// boot-time file that only this machine can write. This pass is the
// gate that makes a declared payload real. The absence of
// modules.conf is how a machine reports that its image predates a
// feature the cluster now declares.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// These are package variables rather than constants, so tests can
// point the actuation at trees of their own making.
var (
	featuresDir     = "/etc/liken/features"
	k3sManifestsDir = "/var/lib/rancher/k3s/server/manifests"
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
			// A slug that this binary's vocabulary does not include.
			// The parser deliberately lets it through (features.go in
			// the cluster package explains why a strict vocabulary
			// would disable a downgraded machine), so the report
			// happens here, naming both plausible causes: this image
			// predates the feature, or a hand-written seed misspelled
			// it.
			status = machine.FeatureStatus{
				Name:  slug,
				State: machine.FeatureMissing,
				Message: fmt.Sprintf(
					"this image's vocabulary has no %q feature; upgrade to a release that carries it, or fix the name if it is a misspelling (this image offers: %s)",
					slug, strings.Join(cluster.FeatureSlugs(), ", ")),
			}
		case paramsErr != nil:
			// A parameter this binary's vocabulary does not include,
			// on a slug it does. The parser lets it through for the
			// same downgrade reason as an unknown slug, and the same
			// two causes apply, so the report happens here too. The
			// feature fails whole, rather than actuating the part of
			// the declaration this image understands.
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

// actuateVendoredFeature makes one declared payload real: it loads
// the payload's modules, runs its boot hook, and seeds its workload
// manifests. Any step can fail without stopping the boot, because a
// machine missing a feature is degraded, not down. The status
// carries the story, and the message names the fix.
func actuateVendoredFeature(moduleBase, slug, machineName string) machine.FeatureStatus {
	status := machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
	dir := filepath.Join(featuresDir, slug)

	// The payload-shipped check. Every image that carries a vendored
	// feature stages its module list here, so the absence of the
	// file means the image itself is older than the feature. Nothing
	// on this machine can repair that; the fix is a release that
	// ships the payload.
	modulesConf := filepath.Join(dir, "modules.conf")
	if _, err := os.Stat(modulesConf); errors.Is(err, fs.ErrNotExist) {
		status.State = machine.FeatureMissing
		status.Message = fmt.Sprintf(
			"this image predates the %s feature; upgrade to a release whose image carries it", slug)
		return status
	}

	// The feature's modules load through the same pipeline as the
	// spec's declared modules, and any verdict short of healthy
	// fails the whole feature: a storage client whose transport is
	// missing is not partly active.
	names, err := readModuleList(modulesConf)
	if err != nil {
		status.State = machine.FeatureFailed
		status.Message = err.Error()
		return status
	}
	for _, m := range loadDeclaredModulesFrom(moduleBase, names) {
		if m.State == machine.ModuleLoaded || m.State == machine.ModuleBuiltin {
			continue
		}
		status.State = machine.FeatureFailed
		status.Message = fmt.Sprintf("module %s: %s", m.Name, m.Message)
		return status
	}

	// The feature's boot hook, for the few files that only the
	// booting machine can write.
	if hook := featureBootHooks[slug]; hook != nil {
		if err := hook(machineName); err != nil {
			status.State = machine.FeatureFailed
			status.Message = err.Error()
			return status
		}
	}

	// The feature's workload, if it has one, joins the manifests k3s
	// applies at startup. seedClusterState already reset the
	// auto-deploy directory to exactly the image's own manifests when
	// clusterState mounted, so this pass adds only what this boot's
	// cluster document declares, and a retracted feature's manifest
	// simply never reappears. The code copies the manifests on every
	// machine, not just leaders, on purpose: only a leader's k3s
	// reads the directory, an extra file on a follower has no
	// effect, and contents that varied by role would be one more
	// thing to reason about during a promotion.
	if err := seedFeatureManifests(slug); err != nil {
		status.State = machine.FeatureFailed
		status.Message = err.Error()
		return status
	}
	return status
}

// actuateWorkloadFeature makes one workload feature real. There is
// no payload gate like the vendored features' modules.conf check: an
// image whose vocabulary knows the slug also carries its manifests,
// because one build produces both. The flux feature also proves its
// configuration first, so a declaration that cannot sync reports the
// missing parameter instead of seeding workloads that would only
// fail in pods.
func actuateWorkloadFeature(clusterDoc *cluster.Cluster, slug string) machine.FeatureStatus {
	status := machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
	if slug == cluster.FeatureFlux {
		if _, err := clusterDoc.FluxConfig(); err != nil {
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

// featureBootHooks are the per-feature boot-time contributions, keyed
// by slug. The iscsi hook writes the initiator's identity. A feature
// with no boot-time file simply has no entry here.
var featureBootHooks = map[string]func(machineName string) error{
	"iscsi": writeInitiatorName,
}

// writeInitiatorName gives the machine its iSCSI identity: the name
// this initiator presents when it logs in to a target, which storage
// arrays use in their access lists. The name derives from the
// machine name, so it stays the same on every boot with nothing to
// persist, and a reinstalled machine comes back as itself. The
// iqn.2026-07.sh.liken prefix follows the iSCSI naming convention: a
// date when the naming authority (the liken.sh domain) was
// registered, then the authority written in reverse, so that names
// under it can never collide with another owner's names. Both halves
// of the initiator read this file: the host's iscsiadm, which CSI
// drivers run through a chroot, directly, and the iscsid DaemonSet
// through its /etc/iscsi hostPath.
func writeInitiatorName(machineName string) error {
	if err := os.MkdirAll(iscsiDir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("InitiatorName=iqn.2026-07.sh.liken:%s\n", machineName)
	return os.WriteFile(filepath.Join(iscsiDir, "initiatorname.iscsi"), []byte(name), 0o600)
}

// featureManifestPaths lists one feature's workload manifests as the
// image ships them. This is the single place that spells out the
// layout (featuresDir/<slug>/manifests/*.yaml), so seeding and
// retraction can never disagree about where a feature's workloads
// live. A feature with no manifests directory has no workload, which
// is fine; nfs takes exactly that shape.
func featureManifestPaths(slug string) ([]string, error) {
	return filepath.Glob(filepath.Join(featuresDir, slug, "manifests", "*.yaml"))
}

// seedFeatureManifests copies a feature's workload manifests into
// k3s's auto-deploy directory.
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
