package main

// What the CLI reads from the cluster before it acts: the
// operator's version from its workload image tag. This operator ships
// one workload, a DaemonSet, so the version is read from it. The label
// and the domain here are the contract the sibling CLIs copy.

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// Where liken installs the operator, and the
	// default namespace the pairing verbs act in.
	operatorNamespace = "liken-system"

	// The label the base binary selects on to find an
	// operator's workload, and this operator's value for it.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "bluetooth"
)

// The label selector for the operator DaemonSet.
var pluginSelector = pluginLabel + "=" + pluginDomain

// operatorVersion reads the version tag off the
// operator DaemonSet's image, the one source every CLI can read with
// no new API. This operator runs one pod per node from a DaemonSet, so
// the workload the label selects is a DaemonSet, not a Deployment.
func operatorVersion(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	daemonSets, err := clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{
		LabelSelector: pluginSelector,
	})
	if err != nil {
		return "", err
	}
	if len(daemonSets.Items) == 0 {
		return "", fmt.Errorf("no DaemonSet carries the label %s", pluginSelector)
	}
	containers := daemonSets.Items[0].Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "", fmt.Errorf("the operator DaemonSet declares no container")
	}
	return imageTag(containers[0].Image), nil
}

// imageTag reads the tag off an image reference, and
// reports an empty tag for a bare name or a digest reference.
func imageTag(image string) string {
	if at := strings.LastIndex(image, "@"); at >= 0 {
		image = image[:at]
	}
	colon := strings.LastIndex(image, ":")
	if colon < 0 || strings.Contains(image[colon+1:], "/") {
		return ""
	}
	return image[colon+1:]
}
