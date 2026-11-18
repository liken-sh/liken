package main

// The Deployments and Services that the operator creates for the
// solvers and the workers, and the pod status that it reads. The pod
// template types are the ones that promotionobjects.go defines for the
// promotion Jobs, so the operator has one pod template type. Each type
// has only the fields that the operator writes or reads, in the JSON
// form of the Kubernetes API.

import (
	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/memo"
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

// GetObjectMeta returns the metadata, for the memo of the operator's
// writes (workload.go).
func (d *deployment) GetObjectMeta() memo.Meta { return &d.Metadata.ObjectMeta }

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

// GetObjectMeta returns the metadata, for the memo of the operator's
// writes (workload.go).
func (s *service) GetObjectMeta() memo.Meta { return &s.Metadata.ObjectMeta }

type serviceSpec struct {
	Selector map[string]string `json:"selector"`
	Ports    []servicePort     `json:"ports"`
}

type servicePort struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
	// TargetPort is the container port's name. The operator always uses
	// a name, never a number, so a string is enough.
	TargetPort string `json:"targetPort"`
}

// ready reports whether the pod has the condition Ready=True, which
// means every container runs and passes its readiness probe. A pod that
// is being deleted is not ready.
func (p *podObject) ready() bool {
	if p.Metadata.DeletionTimestamp != nil {
		return false
	}
	_, ready := condition(p.Status.Conditions, "Ready", "True")
	return ready
}

// waiting returns the scheduler's message when no node fits the pod,
// such as when its claim is bound on another node or does not exist.
// It returns "" when the pod is scheduled.
func (p *podObject) waiting() string {
	scheduled, _ := condition(p.Status.Conditions, "PodScheduled", "False")
	return scheduled.Message
}

// workloadLabels returns the labels of every object that the operator
// creates for a resource. The managed-by label is what the operator's
// watches select. The label for the resource's role and name is what
// the Deployment's selector and the Service select.
func workloadLabels(role, name string) map[string]string {
	return map[string]string{labelManagedBy: managedBy, astrophotography.Group + "/" + role: name}
}

// driverID is the user and group ID of every workload pod. It matches
// the user of observatory-operator's drivers, so the worker can delete a
// centering frame that a driver wrote. The kubelet makes each volume
// writable by this group, as it does for the camera's pod.
const driverID = 1000

func workloadSecurity() *podSecurity {
	return &podSecurity{
		RunAsUser: driverID, RunAsGroup: driverID, RunAsNonRoot: true,
		FSGroup: driverID, FSGroupChangePolicy: "OnRootMismatch",
		SeccompProfile: &seccompProfile{Type: "RuntimeDefault"},
	}
}

// solverSecurity is workloadSecurity without fsGroup.
func solverSecurity() *podSecurity {
	security := workloadSecurity()
	security.FSGroup, security.FSGroupChangePolicy = 0, ""
	return security
}

// restricted is the security context of every workload container. It
// has a read-only root filesystem and drops all capabilities.
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
