package main

// Actuating the cluster's opt-in features on this machine.
//
// The Cluster document's spec.features is the fleet's opt-ins from
// liken's curated vocabulary (the machine package's features.go).
// This pass is init's half of honoring them: one verdict per enabled
// feature, bound for status.features through the facts file, with a
// console line for each so the console and the API tell the same
// story. The cluster document is validated against the vocabulary at
// parse, so every slug that reaches this pass is a known one.
//
// Two kinds of feature arrive here, distinguished by the vocabulary
// table and never by the user. A bundled feature is a component the
// k3s binary already carries; its whole actuation is the disable list
// this boot renders into the k3s drop-in (k3s.go), nothing about it
// can be missing from the image, and it always reports Active. A
// vendored feature is a payload the image ships inert: kernel modules
// listed at /etc/liken/features/<slug>/modules.conf, sometimes a
// workload manifest riding beside them, sometimes a boot-time file
// only this machine can write. This pass is the gate that makes a
// declared payload real, and the modules.conf's absence is how a
// machine reports that its image predates a feature the cluster now
// declares.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrisguidry/liken/machine"
)

// Package variables rather than constants so tests can point the
// actuation at trees of their own making.
var (
	featuresDir     = "/etc/liken/features"
	k3sManifestsDir = "/var/lib/rancher/k3s/server/manifests"
	iscsiDir        = "/etc/iscsi"
)

func actuateFeatures(cluster *machine.Cluster, machineName string) []machine.FeatureStatus {
	slugs := cluster.EnabledFeatures()
	if len(slugs) == 0 {
		return nil
	}
	moduleBase := filepath.Join("/lib/modules", kernelRelease())
	statuses := make([]machine.FeatureStatus, 0, len(slugs))
	for _, slug := range slugs {
		status := machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
		if def := machine.FeatureBySlug(slug); def != nil && def.Kind == machine.FeatureVendored {
			status = actuateVendoredFeature(moduleBase, slug, machineName)
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

// actuateVendoredFeature makes one declared payload real: load its
// modules, run its boot hook, seed its workload manifests. Any step
// can fail without stopping the boot, because a machine missing a
// feature is degraded, not down; the status carries the story, and
// the message names the fix.
func actuateVendoredFeature(moduleBase, slug, machineName string) machine.FeatureStatus {
	status := machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
	dir := filepath.Join(featuresDir, slug)

	// The payload-shipped check. Every image that carries a vendored
	// feature stages its module list here, so the file's absence
	// means the image itself is older than the feature. Nothing on
	// this machine can repair that; the fix is a release that ships
	// the payload.
	modulesConf := filepath.Join(dir, "modules.conf")
	if _, err := os.Stat(modulesConf); errors.Is(err, fs.ErrNotExist) {
		status.State = machine.FeatureMissing
		status.Message = fmt.Sprintf(
			"this image predates the %s feature; upgrade to a release whose image carries it", slug)
		return status
	}

	// The feature's modules load through the same pipeline as the
	// spec's declared modules, and any verdict short of healthy fails
	// the whole feature: a storage client whose transport is missing
	// is not partly active.
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

	// The feature's boot hook, for the few files only the booting
	// machine can write.
	if hook := featureBootHooks[slug]; hook != nil {
		if err := hook(machineName); err != nil {
			status.State = machine.FeatureFailed
			status.Message = err.Error()
			return status
		}
	}

	// The feature's workload, if it carries one, joins the manifests
	// k3s applies at startup. seedClusterState already reset the
	// auto-deploy directory to exactly the image's own manifests when
	// clusterState mounted, so this pass adds only what this boot's
	// cluster document declares, and a retracted feature's manifest
	// simply never reappears. The copy happens on every machine, not
	// just leaders, deliberately: only a leader's k3s reads the
	// directory, an extra file on a follower is inert, and contents
	// that varied by role would be one more thing to reason about
	// during a promotion.
	if err := seedFeatureManifests(dir); err != nil {
		status.State = machine.FeatureFailed
		status.Message = err.Error()
		return status
	}
	return status
}

// featureBootHooks are the per-feature boot-time contributions, keyed
// by slug. The iscsi hook writes the initiator's identity; a feature
// with no boot-time file simply has no entry.
var featureBootHooks = map[string]func(machineName string) error{
	"iscsi": writeInitiatorName,
}

// writeInitiatorName gives the machine its iSCSI identity: the name
// this initiator presents when logging in to a target, which storage
// arrays use in their access lists. It derives from the machine name,
// so it is deterministic on every boot with nothing to persist, and a
// reinstalled machine comes back as itself. The iqn.2026-07.sh.liken
// prefix follows the iSCSI naming convention: a date the naming
// authority (the liken.sh domain) was registered, then the authority
// reversed, so names under it can never collide with another owner's.
// Both halves of the initiator read this file: the host's iscsiadm
// (which CSI drivers exec through a chroot) directly, and the iscsid
// DaemonSet through its /etc/iscsi hostPath.
func writeInitiatorName(machineName string) error {
	if err := os.MkdirAll(iscsiDir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("InitiatorName=iqn.2026-07.sh.liken:%s\n", machineName)
	return os.WriteFile(filepath.Join(iscsiDir, "initiatorname.iscsi"), []byte(name), 0o600)
}

// seedFeatureManifests copies a feature's workload manifests into
// k3s's auto-deploy directory. A feature with no manifests directory
// has no workload, which is fine; nfs, when it arrives, will be
// exactly that shape.
func seedFeatureManifests(dir string) error {
	manifests, err := filepath.Glob(filepath.Join(dir, "manifests", "*.yaml"))
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
