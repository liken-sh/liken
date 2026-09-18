package main

// What the CLI reads from the cluster before it writes: the
// operator's version from its Deployment image tag. The label selector
// and the read here are the contract the sibling CLIs copy.

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	// The label the base binary selects on to find an
	// operator's workload, and this operator's value for it.
	pluginLabel  = "cli.liken.sh/plugin"
	pluginDomain = "library"
)

// The label selector for the operator Deployment.
var pluginSelector = pluginLabel + "=" + pluginDomain

// operatorVersion reads the version tag off the
// operator Deployment's image, the one source every CLI can read with
// no new API.
func operatorVersion(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	deployments, err := clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{
		LabelSelector: pluginSelector,
	})
	if err != nil {
		return "", err
	}
	if len(deployments.Items) == 0 {
		return "", fmt.Errorf("no Deployment carries the label %s", pluginSelector)
	}
	containers := deployments.Items[0].Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return "", fmt.Errorf("the operator Deployment declares no container")
	}
	return imageTag(containers[0].Image), nil
}

// listLibraries lists the Library names in a namespace through a
// dynamic client>
func listLibraries(ctx context.Context, client dynamic.Interface, namespace string) ([]string, error) {
	list, err := client.Resource(libraryResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names, nil
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
