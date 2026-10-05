package main

// The names and labels of the objects the operator creates. Each
// device's pod and Service take the name <kind>-<resource>, such as
// camera-east-main, and each INDI server's pod and Service take the
// name of its owner's kind and name, such as telescope-east. Two kinds
// can hold resources of one name, such as the Telescope east and the
// Guider east, and the kind in the name keeps the objects apart.

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
	// such as a server after a device joined its telescope, is
	// replaced.
	annotationSpec = Group + "/spec"
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
// must be. A resource name can be longer or hold a dot, so a name that
// does not fit is refused with a message.
var serviceName = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// objectName answers the name of the pod and the Service that the
// operator creates for one resource.
func objectName(kind observatory.Kind, name string) (string, error) {
	object := strings.ToLower(kind.Name) + "-" + name
	if len(object) > 63 || !serviceName.MatchString(object) {
		return "", fmt.Errorf("the %s %s needs the Service name %q, which is not a DNS label of 63 characters or fewer", kind.Name, name, object)
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
