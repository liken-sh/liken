package main

// The images the operator stamps into the pods and Jobs it creates
// come from its own pod. The Deployment names the operator image
// once, with a tag, and every companion image is that repository at
// the same tag: library-operator itself for the scanner, and
// library-operator-corrosion and library-operator-media-browser
// beside it. So one pin in a kustomization moves every image
// together, and no manifest names a version twice. SCANNER_IMAGE,
// CORROSION_IMAGE, and BROWSER_IMAGE still win when set, for a test
// or for a cluster whose pod names its image by digest, which has no
// tag to share.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	// The downward API sets this, so the operator can read the pod it
	// runs in. Nothing else tells a container which pod it is.
	podNameVariable = "POD_NAME"
	// The container this binary runs in. Its image is the reference
	// every companion image derives from.
	operatorContainer = "operator"
)

// The three images the operator stamps into the pods and Jobs it
// creates.
type images struct {
	scanner   string
	corrosion string
	browser   string
}

// operatorImages settles each companion image. A variable that is
// set wins. When every variable is set, the operator reads no pod, so
// a cluster with no downward API can still run it. Otherwise it reads
// its own pod and derives the rest from the operator container's
// image.
func operatorImages(ctx context.Context, client *Client, namespace string) (images, error) {
	named := images{
		scanner:   os.Getenv(scannerImageVariable),
		corrosion: os.Getenv(corrosionImageVariable),
		browser:   os.Getenv(browserImageVariable),
	}
	if named.scanner != "" && named.corrosion != "" && named.browser != "" {
		return named, nil
	}
	name := os.Getenv(podNameVariable)
	if name == "" {
		return images{}, fmt.Errorf("%s is unset; the Deployment must name the operator's pod", podNameVariable)
	}
	pod, err := GetPod(ctx, client, namespace, name)
	if err != nil {
		return images{}, fmt.Errorf("reading pod %s/%s: %w", namespace, name, err)
	}
	reference := containerImage(pod, operatorContainer)
	if reference == "" {
		return images{}, fmt.Errorf("pod %s/%s has no container named %s", namespace, name, operatorContainer)
	}
	derived, err := deriveImages(reference)
	if err != nil {
		return images{}, err
	}
	if named.scanner != "" {
		derived.scanner = named.scanner
	}
	if named.corrosion != "" {
		derived.corrosion = named.corrosion
	}
	if named.browser != "" {
		derived.browser = named.browser
	}
	return derived, nil
}

// containerImage returns the image one container of the pod's spec
// names. The spec holds what the manifest stated; the status holds
// what the kubelet resolved, which can differ.
func containerImage(pod *Pod, name string) string {
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return container.Image
		}
	}
	return ""
}

// deriveImages names each companion at the operator's repository and
// tag: the scanner is the operator's own image, and the others take a
// suffix. An image with no tag has no version to share, and the error
// says what to set.
func deriveImages(reference string) (images, error) {
	repository, tag, tagged := splitReference(reference)
	if !tagged {
		return images{}, fmt.Errorf("the operator's image %q has no tag; every companion image takes the tag of this one", reference)
	}
	return images{
		scanner:   reference,
		corrosion: repository + "-corrosion:" + tag,
		browser:   repository + "-media-browser:" + tag,
	}, nil
}

// splitReference takes the repository and the tag apart. Only the
// part after the last "/" can hold a tag: a "@" there is a digest,
// which names no tag, and the last ":" there splits the tag off. A
// ":" before that "/" is a registry port, so it never splits.
func splitReference(reference string) (repository, tag string, tagged bool) {
	name := reference[strings.LastIndex(reference, "/")+1:]
	if strings.Contains(name, "@") {
		return "", "", false
	}
	colon := strings.LastIndex(name, ":")
	if colon <= 0 || colon == len(name)-1 {
		return "", "", false
	}
	cut := len(reference) - len(name) + colon
	return reference[:cut], reference[cut+1:], true
}
