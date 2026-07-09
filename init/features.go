package main

// Actuating the cluster's opt-in features on this machine.
//
// The Cluster document's spec.features is the fleet's opt-ins from
// liken's curated vocabulary (the machine package's features.go).
// This pass is init's report on them: one verdict per enabled
// feature, bound for status.features through the facts file, with a
// console line for each so the console and the API tell the same
// story. The cluster document is validated against the vocabulary at
// parse, so every slug that reaches this pass is a known one.
//
// Every feature in the vocabulary today is a component the k3s
// binary bundles, and a bundled component's whole actuation is the
// disable list this boot renders into the k3s drop-in (k3s.go).
// Nothing about it can be missing from the image, so an enabled
// bundled feature is always Active. Vendored payloads, when the
// vocabulary grows them, actuate here: their kernel modules load
// from /etc/liken/features/<slug>/modules.conf, and that file's
// absence is how a machine reports that its image predates a
// feature the cluster now declares.

import (
	"fmt"
	"strings"

	"github.com/chrisguidry/liken/machine"
)

func actuateFeatures(cluster *machine.Cluster) []machine.FeatureStatus {
	slugs := cluster.EnabledFeatures()
	if len(slugs) == 0 {
		return nil
	}
	statuses := make([]machine.FeatureStatus, 0, len(slugs))
	for _, slug := range slugs {
		status := machine.FeatureStatus{Name: slug, State: machine.FeatureActive}
		fmt.Printf("liken: features: %s: %s\n", status.Name, strings.ToLower(string(status.State)))
		statuses = append(statuses, status)
	}
	return statuses
}
