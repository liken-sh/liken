package main

// The CLI reads the operator's version from the image tag of its
// DaemonSet.

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// The namespace liken installs the operator in. The pairing commands
	// use it as their default namespace.
	operatorNamespace = "liken-system"

	// The liken CLI finds an operator's DaemonSet by this label. This
	// operator's value is bluetooth.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "bluetooth"
)

var pluginSelector = pluginLabel + "=" + pluginDomain

// operatorVersion returns the image tag of the first container of the
// operator's DaemonSet.
func operatorVersion(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	daemonSets, err := clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{
		LabelSelector: pluginSelector,
	})
	if err != nil {
		return "", err
	}
	if len(daemonSets.Items) == 0 {
		return "", fmt.Errorf("no DaemonSet has the label %s", pluginSelector)
	}
	containers := daemonSets.Items[0].Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "", fmt.Errorf("the operator DaemonSet has no container")
	}
	return imageTag(containers[0].Image), nil
}

// imageTag returns the tag of an image reference. It returns an empty
// string for a reference with no tag or with only a digest.
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
