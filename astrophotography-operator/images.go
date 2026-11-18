package main

// The solver and worker pods run the worker image: the operator's image
// repository with the suffix -worker, at the operator's tag. The
// operator reads its own image from its pod, so one tag in a
// kustomization sets both images. library-operator derives its other
// images the same way. ASTROPHOTOGRAPHY_WORKER_IMAGE overrides the
// derivation, and a cluster that sets the operator's image by digest
// needs it, because a digest has no tag to copy.

import (
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const workerImageVariable = "ASTROPHOTOGRAPHY_WORKER_IMAGE"

// workerImage returns the image for the solver and worker pods. The
// operator calls it once, after it becomes the leader. When the
// environment variable is not set, it reads the operator's own pod
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

// deriveWorkerImage returns the worker image in the operator image's
// repository, with the same tag. Only the part after the last "/" can
// hold a tag. A "@" there marks a digest, and a ":" before that "/" is
// a registry port.
func deriveWorkerImage(reference string) (string, error) {
	name := reference[strings.LastIndex(reference, "/")+1:]
	colon := strings.LastIndex(name, ":")
	if strings.Contains(name, "@") || colon <= 0 || colon == len(name)-1 {
		return "", fmt.Errorf("the operator's image %q has no tag, so set %s to the worker's image", reference, workerImageVariable)
	}
	cut := len(reference) - len(name) + colon
	return reference[:cut] + "-worker" + reference[cut:], nil
}
