package kubernetes

// This file reads HelmCharts, the resource k3s uses to install a
// Helm release.
//
// The kind belongs to k3s, not to Kubernetes: the Helm controller
// embedded in the k3s server watches HelmChart resources and renders
// each one into an installed release. k3s deploys its own bundled
// components this way, Traefik among them. The controller puts a
// removal finalizer on every chart it manages, so deleting a
// HelmChart takes two steps: the delete request marks the object,
// and the controller uninstalls the release and then clears the
// mark. No other component clears it, so the machine operator counts
// charts before the helm feature may stop
// (machine-operator/retraction.go).

import "errors"

// HelmChartsPath names the collection, across all namespaces. A chart
// is namespaced, and the ones k3s creates for its own components live
// in kube-system.
const HelmChartsPath = "/apis/helm.cattle.io/v1/helmcharts"

// HelmChart holds the part of the resource that liken reads: where
// the chart lives. What a chart installs stays out of this type. The
// count and the names are enough for a person to act on, and reading
// more would mean holding knowledge of what a Traefik release
// contains, which goes stale the first time another project renames
// something.
type HelmChart struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

// ListHelmCharts reads every HelmChart in the cluster. An API server
// that serves no such kind answers 404 for the whole collection, and
// that answer carries the same meaning as an empty list: the cluster
// holds no charts. So a missing kind returns no charts and no error.
// Every other failure stays an error, because a failed read must
// never reach a caller as an empty cluster.
func ListHelmCharts(c *Client) ([]HelmChart, error) {
	charts, err := List[HelmChart](c, HelmChartsPath)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return charts, err
}
