package main

// The operator reads its own pod to find the images of the pods it
// creates. Each companion image is the operator's repository with a
// suffix, such as media-operator-player, and the operator's tag. So one
// image tag in a kustomization sets the version of every image. The
// variables PLAYER_IMAGE, IDLE_IMAGE, SIDECAR_IMAGE, and DISPLAY_IMAGE
// override the derived images, for tests and for an operator image
// named by digest, which has no tag to share.

import (
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The Deployment sets these variables from the downward API, so the
// operator can read its own pod.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

const operatorContainerName = "operator"

type companionImages struct {
	player  string
	idle    string
	sidecar string
	display string
}

// resolveImages returns the image of each companion. A set variable
// overrides the derived image. When every variable is set, the operator
// does not read its pod, so it runs without the downward API.
func resolveImages(client *apiclient.Client) (companionImages, error) {
	configured := companionImages{
		player:  os.Getenv(playerImageVariable),
		idle:    os.Getenv(idleImageVariable),
		sidecar: os.Getenv(sidecarImageVariable),
		display: os.Getenv(displayImageVariable),
	}
	if configured.player != "" && configured.idle != "" && configured.sidecar != "" && configured.display != "" {
		return configured, nil
	}

	name, namespace := os.Getenv(podNameVariable), os.Getenv(podNamespaceVariable)
	if name == "" || namespace == "" {
		return companionImages{}, fmt.Errorf(
			"%s and %s are unset, so the operator cannot read its own pod to derive %s, %s, %s, and %s",
			podNameVariable, podNamespaceVariable,
			playerImageVariable, idleImageVariable, sidecarImageVariable, displayImageVariable)
	}
	pod, err := GetPod(client, namespace, name)
	if err != nil {
		return companionImages{}, fmt.Errorf("reading the Pod %s/%s: %w", namespace, name, err)
	}
	reference := ""
	for _, container := range pod.Spec.Containers {
		if container.Name == operatorContainerName {
			reference = container.Image
		}
	}
	if reference == "" {
		return companionImages{}, fmt.Errorf(
			"the Pod %s/%s has no container named %s", namespace, name, operatorContainerName)
	}
	derived, err := deriveCompanionImages(reference)
	if err != nil {
		return companionImages{}, err
	}

	if configured.player == "" {
		configured.player = derived.player
	}
	if configured.idle == "" {
		configured.idle = derived.idle
	}
	if configured.sidecar == "" {
		configured.sidecar = derived.sidecar
	}
	if configured.display == "" {
		configured.display = derived.display
	}
	return configured, nil
}

// operatorVersion returns the tag of the operator container's image,
// which the liken_build_info metric reports.
func operatorVersion(client *apiclient.Client) string {
	return containerVersion(client, operatorContainerName)
}

// containerVersion returns the image tag of the named container in this
// process's own pod. Each role reads its version this way, so every
// image of a release reports the same version. It returns an empty
// string when the downward API variables are unset, the pod cannot be
// read, the pod has no such container, or the image has no tag. The
// version is only a metrics label, so it does not fail startup.
func containerVersion(client *apiclient.Client, container string) string {
	name, namespace := os.Getenv(podNameVariable), os.Getenv(podNamespaceVariable)
	if name == "" || namespace == "" {
		return ""
	}
	pod, err := GetPod(client, namespace, name)
	if err != nil {
		return ""
	}
	for _, podContainer := range pod.Spec.Containers {
		if podContainer.Name == container {
			if _, tag, tagged := splitReference(podContainer.Image); tagged {
				return tag
			}
		}
	}
	return ""
}

// deriveCompanionImages fails when the operator's image has no tag, and
// the error says what to set instead.
func deriveCompanionImages(reference string) (companionImages, error) {
	repository, tag, tagged := splitReference(reference)
	if !tagged {
		return companionImages{}, fmt.Errorf(
			"the operator's image %q has no tag, and the companion images take the operator's tag; "+
				"name the image by tag, or set %s, %s, %s, and %s",
			reference, playerImageVariable, idleImageVariable, sidecarImageVariable, displayImageVariable)
	}
	return companionImages{
		player:  repository + "-player:" + tag,
		idle:    repository + "-idle:" + tag,
		sidecar: repository + "-sidecar:" + tag,
		display: repository + "-display:" + tag,
	}, nil
}

// splitReference splits an image reference into repository and tag.
// Only the part after the last "/" can have a tag, because a ":" before
// it separates a registry port. A reference with "@" in that part is a
// digest and has no tag.
func splitReference(reference string) (repository, tag string, tagged bool) {
	last := reference[strings.LastIndex(reference, "/")+1:]
	if strings.Contains(last, "@") {
		return "", "", false
	}
	colon := strings.LastIndex(last, ":")
	if colon < 0 || colon == len(last)-1 {
		return "", "", false
	}
	split := len(reference) - len(last) + colon
	return reference[:split], reference[split+1:], true
}
