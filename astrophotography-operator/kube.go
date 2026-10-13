package main

// The Deployments and Services that the operator creates for the
// solvers and the workers, and what it reads of their pods. The pod's
// parts are the promotion Jobs' types of promotionobjects.go, so a pod
// template has one shape in the operator. Each type holds only the
// fields the operator writes or reads, in the wire format of the
// Kubernetes API.

import (
	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func owner(apiVersion, kind, name, uid string) []ownerReference {
	return []ownerReference{{APIVersion: apiVersion, Kind: kind, Name: name, UID: uid, Controller: true}}
}

// metaOf builds the metadata of an object the operator creates.
func metaOf(name, namespace string, labels map[string]string, owners []ownerReference) objectMeta {
	return objectMeta{ObjectMeta: observatory.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, OwnerReferences: owners}
}

type deployment struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   objectMeta     `json:"metadata"`
	Spec       deploymentSpec `json:"spec"`
}

type deploymentSpec struct {
	Replicas *int32        `json:"replicas"`
	Selector labelSelector `json:"selector"`
	Strategy strategy      `json:"strategy"`
	Template podTemplate   `json:"template"`
}

type labelSelector struct {
	MatchLabels map[string]string `json:"matchLabels"`
}

// strategy is a Deployment's strategy. Recreate stops the old pod
// before it starts the new one.
type strategy struct {
	Type string `json:"type"`
}

type containerPort struct {
	Name          string `json:"name"`
	ContainerPort int32  `json:"containerPort"`
}

type emptyDir struct{}

type probe struct {
	HTTPGet       *httpGet `json:"httpGet,omitempty"`
	PeriodSeconds int32    `json:"periodSeconds,omitempty"`
}

type httpGet struct {
	Path string `json:"path"`
	Port string `json:"port"`
}

type service struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   objectMeta  `json:"metadata"`
	Spec       serviceSpec `json:"spec"`
}

type serviceSpec struct {
	Selector map[string]string `json:"selector"`
	Ports    []servicePort     `json:"ports"`
}

type servicePort struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
	// TargetPort names the container's port. The operator always names
	// it, so the text form is enough.
	TargetPort string `json:"targetPort"`
}

// ready reports whether the kubelet reports the pod Ready: every
// container runs and passes its readiness probe.
func (p *podObject) ready() bool {
	if p.Metadata.DeletionTimestamp != nil {
		return false
	}
	_, ready := condition(p.Status.Conditions, "Ready", "True")
	return ready
}

// waiting answers the scheduler's message while no node fits the pod,
// such as when its claim is bound on another node or missing, and ""
// otherwise.
func (p *podObject) waiting() string {
	scheduled, _ := condition(p.Status.Conditions, "PodScheduled", "False")
	return scheduled.Message
}

// The labels of every object the operator creates for a resource: the
// operator's own label, which its watches select, and the resource
// that the object serves, which the Deployment's selector and the
// Service select.
func workloadLabels(role, name string) map[string]string {
	return map[string]string{labelManagedBy: managedBy, astrophotography.Group + "/" + role: name}
}

// The user and group of every pod the operator runs as a workload:
// 1000, the user of observatory-operator's drivers, so the worker can
// delete a centering frame that a driver wrote. The kubelet gives a
// volume to the group, as it does for the camera's pod.
const driverGroup = 1000

func workloadSecurity() *podSecurity {
	return &podSecurity{
		RunAsUser: driverGroup, RunAsGroup: driverGroup, RunAsNonRoot: true,
		FSGroup: driverGroup, FSGroupChangePolicy: "OnRootMismatch",
		SeccompProfile: &seccompProfile{Type: "RuntimeDefault"},
	}
}

// solverSecurity is workloadSecurity with no fsGroup.
func solverSecurity() *podSecurity {
	security := workloadSecurity()
	security.FSGroup, security.FSGroupChangePolicy = 0, ""
	return security
}

// restricted is the security context of every workload container: a
// read-only root and no capability.
func restricted() *containerSecurity {
	return &containerSecurity{ReadOnlyRootFilesystem: true, Capabilities: &capabilities{Drop: []string{"ALL"}}}
}

func boolPointer(b bool) *bool    { return &b }
func int32Pointer(i int32) *int32 { return &i }
func int64Pointer(i int64) *int64 { return &i }

func deploymentPath(namespace, name string) string {
	return "/apis/apps/v1/namespaces/" + namespace + "/deployments/" + name
}

func servicePath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/services/" + name
}
