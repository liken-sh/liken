package kubernetes

// This file reads the workloads that ship a workstation CLI. An
// operator runs as a Deployment, a DaemonSet, or a StatefulSet, and
// the one that carries a CLI marks itself with the cli.liken.sh/plugin
// label, whose value names the domain (audio, display, media,
// bluetooth, library). The base binary's plugins commands read this
// list, take each workload's container image and its version tag, and
// pull the matching CLI image from the same registry at the same
// version.
//
// The three kinds share one pod-template shape, so one type reads all
// of them. bluetooth-operator ships only a DaemonSet, so a selector
// over Deployments alone would miss it.

import (
	"net/url"
	"strings"
)

// PluginLabel is the label a workload carries to declare that it ships
// a workstation CLI. Its value is the domain.
const PluginLabel = "cli.liken.sh/plugin"

// WorkloadContainer holds the one field the plugins commands read from
// a container: the image reference, whose tag names the version every
// CLI image shares with its operator.
type WorkloadContainer struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// Workload holds the part of a Deployment, DaemonSet, or StatefulSet
// the plugins commands read: which domain the label names, and the
// images its pod template runs. The three kinds carry these fields at
// the same paths, so one type decodes all of them.
type Workload struct {
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				Containers []WorkloadContainer `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

// PluginDomain reports the domain the workload's plugin label names,
// or the empty string when the label is absent.
func (w *Workload) PluginDomain() string {
	return w.Metadata.Labels[PluginLabel]
}

// OperatorImage reports the first container's image, the reference
// whose tag names the operator's version. An operator's pod runs its
// operator as its first container.
func (w *Workload) OperatorImage() string {
	if len(w.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	return w.Spec.Template.Spec.Containers[0].Image
}

// pluginWorkloadKinds names the plural resources the selector reads.
// All three live under the apps/v1 group and carry the pod template at
// the same path.
var pluginWorkloadKinds = []string{"deployments", "daemonsets", "statefulsets"}

// ListPluginWorkloads reads every Deployment, DaemonSet, and
// StatefulSet across the cluster that carries the plugin label, then
// keeps one workload per domain. The label selector names only the
// key, so a workload with any value for it is returned. The collection
// with no namespace segment spans every namespace.
func ListPluginWorkloads(c *Client) ([]Workload, error) {
	var all []Workload
	for _, kind := range pluginWorkloadKinds {
		path := "/apis/apps/v1/" + kind + "?labelSelector=" + url.QueryEscape(PluginLabel)
		workloads, err := List[Workload](c, path)
		if err != nil {
			return nil, err
		}
		all = append(all, workloads...)
	}
	return dedupeByDomain(all), nil
}

// dedupeByDomain keeps one workload per domain. Two workloads may
// carry the same domain label, so the choice prefers the one whose
// image path names the domain's operator, and otherwise keeps the
// first seen. The read order (deployments, daemonsets, statefulsets)
// makes the fallback stable.
func dedupeByDomain(workloads []Workload) []Workload {
	index := map[string]int{}
	result := []Workload{}
	for _, w := range workloads {
		domain, image := w.PluginDomain(), w.OperatorImage()
		if domain == "" || image == "" {
			continue
		}
		i, seen := index[domain]
		if !seen {
			index[domain] = len(result)
			result = append(result, w)
			continue
		}
		if imageNamesOperator(image, domain) && !imageNamesOperator(result[i].OperatorImage(), domain) {
			result[i] = w
		}
	}
	return result
}

// imageNamesOperator reports whether an image path names the domain's
// operator, the signal that picks between two workloads that share a
// domain label. An operator image is named <domain>-operator.
func imageNamesOperator(image, domain string) bool {
	return strings.Contains(image, domain+"-operator")
}
