package main

// The promotion Jobs run the operator's binary, so they run the
// operator's image. The operator reads that image from its own pod, so
// only the operator's Deployment sets it. images.go derives the worker
// image from it.

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// operatorContainer is the name of the container in
// deploy/operator.yaml that runs this binary.
const operatorContainer = "operator"

// ownImage reads the image of the operator's container from the
// operator's own pod.
func ownImage(client *apiclient.Client, namespace, pod string) (string, error) {
	operatorPod, err := apiclient.Get[podObject](client, "/api/v1/namespaces/"+namespace+"/pods/"+pod)
	if err != nil {
		return "", fmt.Errorf("reading the operator's pod %s/%s: %w", namespace, pod, err)
	}
	for _, c := range operatorPod.Spec.Containers {
		if c.Name == operatorContainer {
			return c.Image, nil
		}
	}
	return "", fmt.Errorf("the operator's pod %s/%s has no container %s", namespace, pod, operatorContainer)
}

// ownImage returns the operator's image for the promotion Jobs. It
// reads the pod once and caches the result, because a container's image
// cannot change in a running pod.
func (p *promotions) ownImage() (string, error) {
	if p.image != "" {
		return p.image, nil
	}
	image, err := ownImage(p.client, p.namespace, p.pod)
	if err == nil {
		p.image = image
	}
	return image, err
}
