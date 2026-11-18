package main

// Each pod and Service that the operator creates is named
// <resource-name>-<kind>: the Camera east-main runs in the pod
// east-main-camera, and the INDI server of the Telescope east runs in
// the pod east-telescope. The resource name comes first, so
// kubectl get pods lists one telescope's pods together. The kind suffix
// keeps the Mount east and the Telescope east apart, and because no
// kind name contains "-", two resources never get the same name.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The watches select pods and Services by labelManagedBy, so the
// operator reads only its own objects.
const (
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelPartOf    = "app.kubernetes.io/part-of"
	// labelName is the label that each Service selects its pod by.
	labelName = "app.kubernetes.io/name"
	labelRole = Group + "/role"
	// labelServer names the INDI server of a pod: the server's own pod,
	// or the server that connects to a device pod.
	labelServer = Group + "/server"
	// labelKind and labelResource name the resource that the operator
	// created the object for.
	labelKind     = Group + "/kind"
	labelResource = Group + "/resource"

	managedBy  = "observatory-operator"
	partOf     = "observatory"
	roleServer = "server"
	roleDevice = "device"

	// annotationSpec is a digest of the pod spec. The operator replaces
	// a pod whose digest differs from the digest of the spec it would
	// build now.
	annotationSpec = Group + "/spec"
	// annotationDrivers lists the devices of a server, one address per
	// line, such as east-mount:7625. The server's pod mounts it as a
	// file, and its shim starts and stops drivers to match the list.
	annotationDrivers = Group + "/drivers"
)

const Group = observatory.Group

// indiserver listens on INDI's standard port. socat in each device pod
// listens on the next port.
const (
	serverPort = 7624
	devicePort = 7625
)

// serviceName matches a DNS-1035 label, which a Service name must be.
// A resource name can contain a dot or start with a digit, so a
// generated name can fail it.
var serviceName = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// maxName is the longest DNS label.
const maxName = 63

// generatedName returns the name of a resource's pod and Service,
// without validating it.
func generatedName(kind observatory.Kind, name string) string {
	return name + "-" + strings.ToLower(kind.Name)
}

// objectName returns the name of a resource's pod and Service, or an
// error that says how to change the resource name.
func objectName(kind observatory.Kind, name string) (string, error) {
	object := generatedName(kind, name)
	if len(object) > maxName {
		return "", fmt.Errorf("the pod and Service name %q has %d characters, and Kubernetes allows %d: shorten the name of the %s to %d characters or fewer",
			object, len(object), maxName, kind.Name, maxName-len(object)+len(name))
	}
	if !serviceName.MatchString(object) {
		return "", fmt.Errorf(`the pod and Service name %q is not a DNS label: use only lowercase letters, digits, and "-", and start with a letter`, object)
	}
	return object, nil
}

func labels(object, role, server string, kind observatory.Kind, resource string) map[string]string {
	return map[string]string{
		labelManagedBy: managedBy,
		labelPartOf:    partOf,
		labelName:      object,
		labelRole:      role,
		labelServer:    server,
		labelKind:      kind.Name,
		labelResource:  resource,
	}
}

func serviceHost(name, namespace string) string {
	return name + "." + namespace + ".svc"
}
