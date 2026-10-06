package main

// The names and labels of the objects the operator creates. Each pod
// and Service takes the name <resource-name>-<kind>: the Camera
// east-main runs in the pod east-main-camera, and the INDI server of the
// Telescope east runs in the pod east-telescope. The resource's name
// comes first, so kubectl get pods lists one telescope's objects
// together. The kind keeps apart two resources of one name, such as the
// Mount east and the Telescope east. A kind's name holds no "-", so two
// resources never share a generated name.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The labels on every object the operator creates. The watches select
// pods and Services by managedBy, so the operator reads its own objects
// and no others.
const (
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelPartOf    = "app.kubernetes.io/part-of"
	// labelName is the selector of each object's Service, as in the
	// manifests of plan 03.
	labelName = "app.kubernetes.io/name"
	// labelRole is server or device.
	labelRole = Group + "/role"
	// labelServer names the INDI server a pod belongs to: the server's
	// own pod, and each device pod that it reaches through a shim.
	labelServer = Group + "/server"
	// labelKind and labelResource name the resource that caused the
	// object.
	labelKind     = Group + "/kind"
	labelResource = Group + "/resource"

	managedBy  = "observatory-operator"
	partOf     = "observatory"
	roleServer = "server"
	roleDevice = "device"

	// annotationSpec holds a digest of the pod the operator built. A
	// pod whose digest differs from the pod the operator builds now,
	// such as one from an older image, is replaced.
	annotationSpec = Group + "/spec"
	// annotationDrivers lists the devices of a server, one address on
	// each line, such as east-mount:7625. The server's pod reads it as
	// a file, and its shim starts and stops each driver to match
	// (drivers.go).
	annotationDrivers = Group + "/drivers"
)

// Group is the API group of the observatory's resources.
const Group = observatory.Group

// The ports of plan 03: indiserver listens on INDI's own port, and
// socat in each device pod listens on the next one.
const (
	serverPort = 7624
	devicePort = 7625
)

// serviceName matches a DNS-1035 label, which is what a Service name
// must be. A resource name can hold a dot or start with a digit, so a
// generated name that does not match is refused with a message.
var serviceName = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// maxName is the longest DNS label. The shims dial each device by its
// Service name, so a longer name cannot be used.
const maxName = 63

// generatedName answers the name of the pod and the Service that the
// operator creates for one resource, with no check.
func generatedName(kind observatory.Kind, name string) string {
	return name + "-" + strings.ToLower(kind.Name)
}

// objectName answers the name of the pod and the Service that the
// operator creates for one resource. The error states how to fix a
// resource's name when the generated name is no Service name.
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

// labels answers the labels of one object the operator creates.
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

// serviceHost answers the DNS name of a Service in the cluster.
func serviceHost(name, namespace string) string {
	return name + "." + namespace + ".svc"
}
