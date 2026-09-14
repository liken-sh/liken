package main

// The images the operator stamps into the pods it creates come from
// its own pod. The Deployment names the operator image once, with a
// tag, and every companion image is that repository with a suffix at
// the same tag: media-operator-player, media-operator-idle,
// media-operator-sidecar, and media-operator-osd beside
// media-operator. So one pin in a kustomization moves every image
// together, and no manifest names a version twice. PLAYER_IMAGE,
// IDLE_IMAGE, SIDECAR_IMAGE, and OSD_IMAGE still win when set, for a
// test or for a cluster whose pod names its image by digest, which has
// no tag to share.

import (
	"fmt"
	"os"
	"strings"
)

// The downward API sets these two, so the operator can read the pod
// it runs in. Nothing else tells a container which pod it is.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

// The container this binary runs in. Its image is the reference
// every companion image derives from.
const operatorContainerName = "operator"

// The four images the operator stamps into the pods it creates.
type companionImages struct {
	player  string
	idle    string
	sidecar string
	osd     string
}

// resolveImages settles each companion image. A variable that is set
// wins. When every variable is set, the operator reads no pod, so a
// cluster with no downward API can still run it. Otherwise it reads
// its own pod and derives the rest from the operator container's
// image.
func resolveImages(client *Client) (companionImages, error) {
	stated := companionImages{
		player:  os.Getenv(playerImageVariable),
		idle:    os.Getenv(idleImageVariable),
		sidecar: os.Getenv(sidecarImageVariable),
		osd:     os.Getenv(osdImageVariable),
	}
	if stated.player != "" && stated.idle != "" && stated.sidecar != "" && stated.osd != "" {
		return stated, nil
	}

	name, namespace := os.Getenv(podNameVariable), os.Getenv(podNamespaceVariable)
	if name == "" || namespace == "" {
		return companionImages{}, fmt.Errorf(
			"%s and %s are unset, so the operator cannot read its own pod to derive %s, %s, %s, and %s",
			podNameVariable, podNamespaceVariable,
			playerImageVariable, idleImageVariable, sidecarImageVariable, osdImageVariable)
	}
	pod, err := GetPod(client, namespace, name)
	if err != nil {
		return companionImages{}, fmt.Errorf("reading pod %s/%s: %w", namespace, name, err)
	}
	reference := ""
	for _, container := range pod.Spec.Containers {
		if container.Name == operatorContainerName {
			reference = container.Image
		}
	}
	if reference == "" {
		return companionImages{}, fmt.Errorf(
			"pod %s/%s has no container named %s", namespace, name, operatorContainerName)
	}
	derived, err := deriveCompanionImages(reference)
	if err != nil {
		return companionImages{}, err
	}

	if stated.player == "" {
		stated.player = derived.player
	}
	if stated.idle == "" {
		stated.idle = derived.idle
	}
	if stated.sidecar == "" {
		stated.sidecar = derived.sidecar
	}
	if stated.osd == "" {
		stated.osd = derived.osd
	}
	return stated, nil
}

// operatorVersion reads this operator's own pod and answers the tag its
// own image names, the same fact resolveImages reads to derive the
// companion images. liken_build_info reports this as the release the
// process runs. A downward API the Deployment did not set, a pod the
// API server no longer holds, or an image named by digest instead of
// a tag all answer the empty string, because metrics are secondary to
// the reconcile loop and never worth failing startup over.
func operatorVersion(client *Client) string {
	name, namespace := os.Getenv(podNameVariable), os.Getenv(podNamespaceVariable)
	if name == "" || namespace == "" {
		return ""
	}
	pod, err := GetPod(client, namespace, name)
	if err != nil {
		return ""
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == operatorContainerName {
			if _, tag, tagged := splitReference(container.Image); tagged {
				return tag
			}
		}
	}
	return ""
}

// deriveCompanionImages names each companion as the operator's
// repository with a suffix, at the operator's tag. An image with no
// tag has no version to share, and the error says what to set.
func deriveCompanionImages(reference string) (companionImages, error) {
	repository, tag, tagged := splitReference(reference)
	if !tagged {
		return companionImages{}, fmt.Errorf(
			"the operator's image %q names no tag, and the companion images take the operator's tag; "+
				"name the image by tag, or set %s, %s, %s, and %s",
			reference, playerImageVariable, idleImageVariable, sidecarImageVariable, osdImageVariable)
	}
	return companionImages{
		player:  repository + "-player:" + tag,
		idle:    repository + "-idle:" + tag,
		sidecar: repository + "-sidecar:" + tag,
		osd:     repository + "-osd:" + tag,
	}, nil
}

// splitReference takes the repository and the tag apart. Only the
// part after the last "/" can hold a tag: a "@" there is a digest,
// which names no tag, and the last ":" there splits the tag off. A
// ":" before that "/" is a registry port, so it never splits.
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
