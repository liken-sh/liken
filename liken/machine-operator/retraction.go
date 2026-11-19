package main

// The retraction barrier keeps a feature running after an edit removes
// it from the cluster document, until the cluster has no objects left
// that only the feature's controller can finish deleting.
//
// When an edit removes a feature, init stops rendering its
// configuration and its controller does not start at the next boot.
// Some objects that the controller created can be deleted only while
// it runs:
//
//   - k3s deploys Traefik through a HelmChart, and the Helm controller
//     adds a finalizer to every chart it manages. k3s deletes the
//     HelmChart when traefik is disabled. If the Helm controller has
//     already stopped, nothing removes the finalizer, the HelmChart is
//     never deleted, and the release keeps running. Only traefik
//     requires helm, so an edit that removes traefik also removes helm.
//   - The servicelb cloud controller adds a cleanup finalizer to every
//     LoadBalancer Service. A Service deleted after that controller
//     stops is never removed.
//
// So on every pass the operator adds back to the desired document each
// removed feature whose precondition the cluster does not satisfy yet,
// and hashes, stages, and boots that reduced document. A machine that
// reboots during a retraction starts with the removed feature still
// running.
//
// Dependent features need no ordering rule. When an edit removes
// traefik and helm together, the HelmCharts still exist, so the pass
// keeps helm and stops only traefik. k3s then deletes the charts, the
// Helm controller uninstalls the releases, and a later pass finds no
// charts and stops helm.
//
// The cluster document has no field that records a kept feature. Every machine
// reads the same objects and evaluates the same preconditions, so every
// machine stages the same reduced document.

import (
	"fmt"
	"maps"
	"strings"

	"github.com/liken-sh/liken/liken/cluster"
)

// A featureHold is a feature that keeps running after an edit removed
// it, because the cluster still has objects that only its
// controller can delete. blocker describes those objects for a person
// to act on.
type featureHold struct {
	slug    string
	blocker string
}

// preconditionEvaluator checks one precondition against the live
// cluster, and when it is not satisfied, describes the objects that
// block it. reduceRetraction takes it as a parameter so that tests can
// pass a stub.
type preconditionEvaluator func(cluster.Precondition) (satisfied bool, blocker string, err error)

// reduceRetraction derives the document this machine stages from the
// document the deployment wrote. It adds back to spec.features every
// retracted feature whose precondition the cluster does not satisfy,
// with its configuration from bootDoc, and returns a featureHold for
// each one. When the edit retracts no feature, it makes no API calls.
func reduceRetraction(bootDoc, desired *cluster.Cluster, evaluate preconditionEvaluator) (*cluster.Cluster, []featureHold) {
	if bootDoc == nil {
		// Without the boot document no retraction can be found. When
		// the boot ran a cluster document that cannot be read, the
		// convergence decision (cluster.go) reports FactsIncomplete and
		// stages nothing, so the desired document does not bypass the
		// barrier. When the boot ran no cluster document, no feature
		// was running.
		return desired, nil
	}
	reduced := desired
	var held []featureHold
	for _, slug := range cluster.RetractedFeatures(bootDoc, desired) {
		def := cluster.FeatureBySlug(slug)
		if def == nil || def.Retraction.Precondition == cluster.PreconditionNone {
			continue
		}
		satisfied, blocker, err := evaluate(def.Retraction.Precondition)
		if err != nil {
			// A failed check keeps the feature, and the next pass checks
			// again. Stopping it could leave finalizers that nothing
			// removes.
			blocker = fmt.Sprintf("the check for objects that depend on it did not run: %v", err)
		} else if satisfied {
			continue
		}
		reduced = withFeature(reduced, slug, bootDoc.Spec.Features[slug])
		held = append(held, featureHold{slug: slug, blocker: blocker})
	}
	return reduced, held
}

// withFeature returns a copy of doc with one more feature declared. It
// copies because doc is the live Cluster that the rest of the pass also
// reads.
//
// cfg is the feature's declaration in the boot document, so a kept
// feature keeps its parameters. A feature that ran only because
// another feature required it has no declaration, so cfg is nil and the
// feature is declared as {}.
func withFeature(doc *cluster.Cluster, slug string, cfg *cluster.FeatureConfig) *cluster.Cluster {
	reduced := *doc
	reduced.Spec.Features = maps.Clone(doc.Spec.Features)
	if reduced.Spec.Features == nil {
		reduced.Spec.Features = map[string]*cluster.FeatureConfig{}
	}
	if cfg == nil {
		cfg = &cluster.FeatureConfig{}
	}
	reduced.Spec.Features[slug] = cfg
	return &reduced
}

// evaluatePrecondition checks one precondition against the live
// cluster. A precondition is satisfied when no object of its kind
// remains, and the blocker lists the objects that remain.
func evaluatePrecondition(r *reader, p cluster.Precondition) (bool, string, error) {
	switch p {
	case cluster.NoHelmCharts:
		charts, err := r.helmCharts()
		if err != nil {
			return false, "", err
		}
		if len(charts) == 0 {
			return true, "", nil
		}
		names := make([]string, len(charts))
		for i, chart := range charts {
			names[i] = chart.Metadata.Namespace + "/" + chart.Metadata.Name
		}
		return false, fmt.Sprintf(
			"HelmCharts (%s) remain in the cluster, and the Helm controller stops only after they are deleted and their releases are uninstalled",
			nameList(names)), nil

	case cluster.NoLoadBalancerServices:
		services, err := r.loadBalancerServices()
		if err != nil {
			return false, "", err
		}
		if len(services) == 0 {
			return true, "", nil
		}
		names := make([]string, len(services))
		for i, s := range services {
			names[i] = s.Metadata.Namespace + "/" + s.Metadata.Name
		}
		return false, fmt.Sprintf(
			"Services of type LoadBalancer (%s) remain in the cluster, and the controller that cleans them up stops only after they are deleted",
			nameList(names)), nil
	}
	// A precondition in the feature table that this switch does not
	// handle keeps the feature running.
	return false, fmt.Sprintf("this build has no check for the %s precondition", p), nil
}

// nameListLimit is the number of object names that a blocker lists,
// so that a condition message stays short.
const nameListLimit = 3

func nameList(names []string) string {
	if len(names) <= nameListLimit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:nameListLimit], ", "), len(names)-nameListLimit)
}

// holdMessage is the condition message for a blocked retraction.
func holdMessage(held []featureHold) string {
	sentences := make([]string, len(held))
	for i, h := range held {
		sentences[i] = fmt.Sprintf("%s cannot stop yet: %s", h.slug, h.blocker)
	}
	return strings.Join(sentences, "; ")
}
