package main

// The promotion Jobs run the operator's own binary, so they run the
// operator's own image, and the solvers and workers run the image beside
// it with the same tag. The Deployment names the image once, and the
// operator reads it from its own pod, so one pin in a kustomization
// moves the operator and its Jobs together.

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// operatorContainer is the container of deploy/operator.yaml that runs
// this binary.
const operatorContainer = "operator"

// ownImage answers the image of the operator's container, from its own
// pod. The promotion Jobs run it, and the worker image takes its tag
// (images.go).
func ownImage(client *apiclient.Client, namespace, pod string) (string, error) {
	held, err := apiclient.Get[podObject](client, "/api/v1/namespaces/"+namespace+"/pods/"+pod)
	if err != nil {
		return "", fmt.Errorf("reading the operator's pod %s/%s: %w", namespace, pod, err)
	}
	for _, c := range held.Spec.Containers {
		if c.Name == operatorContainer {
			return c.Image, nil
		}
	}
	return "", fmt.Errorf("the operator's pod %s/%s has no container %s", namespace, pod, operatorContainer)
}

// ownImage answers the operator's image for the promotion Jobs, and
// keeps it, because a pod's image never changes.
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
