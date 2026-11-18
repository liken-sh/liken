package main

// The scan pods run firmware-operator-scan at the operator's own tag.
// kustomize's newTag rewrites a container's image field but not a flag,
// so when --scan-image is not set, the operator reads its own pod and
// appends -scan to the repository of its image. One newTag then sets
// both images. --scan-image is for a test, and for an operator image
// referenced by digest, which has no tag to copy.

import (
	"fmt"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// operatorContainer is the operator's container in deploy/operator.yaml.
const operatorContainer = "operator"

func resolveScanImage(client *apiclient.Client, flag, namespace, name string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	own, err := apiclient.Get[pod](client, podPath(namespace, name))
	if err != nil {
		return "", fmt.Errorf("reading the Pod %s/%s of the operator: %w", namespace, name, err)
	}
	for _, c := range own.Spec.Containers {
		if c.Name == operatorContainer {
			return scanImageFor(c.Image)
		}
	}
	return "", fmt.Errorf("the Pod %s/%s has no container named %s", namespace, name, operatorContainer)
}

func scanImageFor(reference string) (string, error) {
	last := strings.LastIndex(reference, "/") + 1
	colon := strings.LastIndex(reference[last:], ":")
	if strings.Contains(reference[last:], "@") || colon < 1 || last+colon == len(reference)-1 {
		return "", fmt.Errorf("the operator's image %q has no tag for the scan image to use, so set --scan-image", reference)
	}
	split := last + colon
	return reference[:split] + "-scan" + reference[split:], nil
}
