package main

// The promotion Jobs run the operator's own binary, so they run the
// operator's own image. The Deployment names the image once, and the
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
// pod, and keeps it, because a pod's image never changes.
func (p *promotions) ownImage() (string, error) {
	if p.image != "" {
		return p.image, nil
	}
	pod, err := apiclient.Get[podObject](p.client, "/api/v1/namespaces/"+p.namespace+"/pods/"+p.pod)
	if err != nil {
		return "", fmt.Errorf("reading the operator's pod %s/%s: %w", p.namespace, p.pod, err)
	}
	for _, c := range pod.Spec.Containers {
		if c.Name == operatorContainer {
			p.image = c.Image
			return p.image, nil
		}
	}
	return "", fmt.Errorf("the operator's pod %s/%s has no container %s", p.namespace, p.pod, operatorContainer)
}
