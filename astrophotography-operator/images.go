package main

// The image of the solver's and the workers' pods comes from the
// operator's own pod. The Deployment names the operator's image once,
// with a tag, and the worker image is the same repository with the
// suffix -worker at the same tag, as library-operator derives its
// companions. So one pin in a kustomization moves both images
// together. ASTROPHOTOGRAPHY_WORKER_IMAGE wins when it is set, for a
// cluster whose pod names its image by digest, which has no tag to
// share.

import (
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const workerImageVariable = "ASTROPHOTOGRAPHY_WORKER_IMAGE"

// workerImage answers the image of the solver's and the workers' pods.
// It reads the operator's own pod once, at the start of the lead
// (ownimage.go).
func workerImage(client *apiclient.Client, namespace, podName string) (string, error) {
	if image := os.Getenv(workerImageVariable); image != "" {
		return image, nil
	}
	image, err := ownImage(client, namespace, podName)
	if err != nil {
		return "", err
	}
	return deriveWorkerImage(image)
}

// deriveWorkerImage names the worker image at the operator's repository
// and tag. Only the part after the last "/" can hold a tag: a "@" there
// is a digest, and a ":" before that "/" is a registry's port.
func deriveWorkerImage(reference string) (string, error) {
	name := reference[strings.LastIndex(reference, "/")+1:]
	colon := strings.LastIndex(name, ":")
	if strings.Contains(name, "@") || colon <= 0 || colon == len(name)-1 {
		return "", fmt.Errorf("the operator's image %q has no tag; set %s to the worker's image", reference, workerImageVariable)
	}
	cut := len(reference) - len(name) + colon
	return reference[:cut] + "-worker" + reference[cut:], nil
}
