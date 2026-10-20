package main

// The scan pods run the image firmware-operator-scan, at the operator's
// own tag. A kustomization pins a component's images with newTag in its
// images list, and kustomize sets that tag only in a container's image
// field, not in a flag. So with no --scan-image, the operator reads its
// own pod and names the scan image from its own: the same repository
// with -scan added, at the same tag. One pin then moves both images
// together. --scan-image wins when it is set, for a test or for an
// operator image named by digest, which has no tag to share.

import (
	"fmt"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// operatorContainer is the container of the operator in its own pod.
const operatorContainer = "operator"

// resolveScanImage answers the scan image: the flag's value when it is
// set, and the image derived from the operator's own otherwise.
func resolveScanImage(client *apiclient.Client, flag, namespace, name string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	own, err := apiclient.Get[pod](client, podPath(namespace, name))
	if err != nil {
		return "", fmt.Errorf("reading the operator's own pod %s/%s for its image: %w", namespace, name, err)
	}
	for _, held := range own.Spec.Containers {
		if held.Name == operatorContainer {
			return scanImageFor(held.Image)
		}
	}
	return "", fmt.Errorf("the pod %s/%s has no container named %s", namespace, name, operatorContainer)
}

// scanImageFor names the scan image beside an operator image.
func scanImageFor(reference string) (string, error) {
	last := strings.LastIndex(reference, "/") + 1
	colon := strings.LastIndex(reference[last:], ":")
	if strings.Contains(reference[last:], "@") || colon < 1 || last+colon == len(reference)-1 {
		return "", fmt.Errorf("the operator's image %q names no tag for the scan image to share; set --scan-image", reference)
	}
	split := last + colon
	return reference[:split] + "-scan" + reference[split:], nil
}
