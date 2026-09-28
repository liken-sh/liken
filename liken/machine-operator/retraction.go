package main

// The retraction barrier: what must be true before a feature stops.
//
// A feature's controller runs because the cluster document declares
// the feature. When the document stops declaring it, init stops
// rendering its configuration and the controller does not start at
// the next boot. What that controller programmed stays, and some of
// it can only be removed by the controller itself.
//
// Two features carry the consequence. k3s deploys Traefik through a
// HelmChart, and the Helm controller puts a removal finalizer on
// every chart it manages. Nothing but traefik requires helm, so an
// edit that removes traefik removes helm with it. k3s deletes the
// HelmChart once the disable list names traefik, and if the Helm
// controller has already stopped, the deletion never completes: the
// finalizer stays, and the release keeps running. servicelb has the
// same problem, over objects a deployment owns. The cloud controller
// inside it holds the cleanup finalizer on every LoadBalancer
// Service, so a Service deleted after that controller stops also
// never finishes deleting.
//
// So a retraction waits for the cluster instead of applying halfway.
// On every pass the operator derives the document it stages: the
// cluster document as the deployment wrote it, with every feature put
// back whose precondition the cluster does not satisfy yet. That
// reduced document is what the operator hashes, stages, and boots
// under. A machine that reboots in the middle of a retraction comes
// back up running the held feature, so the controller runs for as
// long as its programming is in force.
//
// The order between dependent features needs no rule of its own. An
// edit that removes traefik retracts helm too, and the HelmCharts
// still exist at that moment, so the pass holds helm and stops
// traefik alone. k3s deletes the charts, the Helm controller
// uninstalls the releases, and the next pass reads no charts and
// stops helm. Nothing here consults the dependency graph.
//
// This is the machine operator's work because the machine operator
// selects the document each machine stages. There is no handshake
// between operators and no field in the cluster document for a held
// feature. Every machine reads the same objects, evaluates the same
// preconditions, and derives the same reduced document.

import (
	"fmt"
	"maps"
	"strings"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
)

// A featureHold is one feature that keeps running although the
// document no longer declares it, because the cluster still holds
// objects that only this feature's controller can remove. Blocker
// names those objects, in a sentence a person can act on.
type featureHold struct {
	slug    string
	blocker string
}

// preconditionEvaluator answers one feature's precondition against
// the live cluster: whether it holds, and when it does not, what
// still stands in the way. The reduction takes this as a parameter
// rather than calling the API itself, so the decision stays a pure
// function over its inputs, the way every other decision in this
// operator is.
type preconditionEvaluator func(cluster.Precondition) (holds bool, blocker string, err error)

// reduceRetraction derives the document this machine stages from the
// document the deployment wrote. Every feature whose precondition the
// cluster does not satisfy goes back into spec.features, and the
// caller receives those features and the reason for each.
//
// An edit that stops no feature evaluates nothing, so the ordinary
// pass, where the document is unchanged or changes something else,
// costs no API calls.
//
// A held feature goes back with the declaration it runs under now,
// which withFeature takes from the boot document.
func reduceRetraction(bootDoc, desired *cluster.Cluster, evaluate preconditionEvaluator) (*cluster.Cluster, []featureHold) {
	if bootDoc == nil {
		// A retraction is a difference between two documents, and
		// without the document this boot ran there is no difference to
		// measure. The convergence decision applies an unreadable boot
		// document by rebooting (cluster.go), and a reboot is also the
		// safest way to stop a controller.
		return desired, nil
	}
	reduced := desired
	var held []featureHold
	for _, slug := range cluster.RetractedFeatures(bootDoc, desired) {
		def := cluster.FeatureBySlug(slug)
		if def == nil || def.Retraction.Precondition == cluster.PreconditionNone {
			continue
		}
		holds, blocker, err := evaluate(def.Retraction.Precondition)
		if err != nil {
			// A failed read is not a satisfied precondition. A feature
			// held on a failed read costs a delayed retraction, and the
			// next pass evaluates it again. A feature stopped on a
			// failed read costs the stranded objects this barrier
			// prevents.
			blocker = fmt.Sprintf("the check for what still depends on it could not run: %v", err)
		} else if holds {
			continue
		}
		reduced = withFeature(reduced, slug, bootDoc.Spec.Features[slug])
		held = append(held, featureHold{slug: slug, blocker: blocker})
	}
	return reduced, held
}

// withFeature returns a copy of the document with one more feature
// declared. It copies rather than edits, because the document it
// receives is the live Cluster that the rest of the pass reads: the
// release feed comes from that same object, and the reduction must
// change nothing that anything else reads.
//
// The configuration comes from the document this boot ran under, so a
// held feature keeps running exactly as it has been running. A
// parameterized feature declared with {} instead would come back
// stripped of its parameters, which is a different feature from the
// one the machine is holding. A requirement that nobody declared has
// no configuration of its own, and {} is right for it.
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

// evaluatePrecondition answers one precondition against the live
// cluster. Every precondition is a count of objects that only the
// feature's own controller can finish removing, so an unsatisfied
// precondition always carries the names of those objects.
func evaluatePrecondition(c *kubernetes.Client, p cluster.Precondition) (bool, string, error) {
	switch p {
	case cluster.NoHelmCharts:
		charts, err := kubernetes.ListHelmCharts(c)
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
			"the cluster still holds HelmCharts (%s); they must be deleted, and their releases uninstalled, before the Helm controller stops",
			nameList(names)), nil

	case cluster.NoLoadBalancerServices:
		services, err := kubernetes.ListLoadBalancerServices(c)
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
			"the cluster still holds Services of type LoadBalancer (%s); they must be deleted before the controller that cleans them up stops",
			nameList(names)), nil
	}
	// The vocabulary is compiled in, so a precondition this switch
	// does not cover is one the feature table states and this file
	// has no case for. Reading it as unsatisfied is the safe
	// direction, and the feature keeps running.
	return false, fmt.Sprintf("this build cannot evaluate the %s precondition", p), nil
}

// nameListLimit is how many object names a blocker carries. A
// condition message is written for a person, who acts on the first
// few names, and every name after them costs message length without
// changing the correction.
const nameListLimit = 3

func nameList(names []string) string {
	if len(names) <= nameListLimit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:nameListLimit], ", "), len(names)-nameListLimit)
}

// holdMessage is the condition's message for a retraction that
// cannot proceed: each feature that keeps running, and the objects
// the cluster must lose before that feature may stop.
func holdMessage(held []featureHold) string {
	sentences := make([]string, len(held))
	for i, h := range held {
		sentences[i] = fmt.Sprintf("%s cannot stop yet: %s", h.slug, h.blocker)
	}
	return strings.Join(sentences, "; ")
}
