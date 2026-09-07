package main

// The progress pod is one standing pod per namespace, owned by the
// Catalog, holding the namespace's durable progress store on a claim of
// its own and writing to it from the bus. It is the standing member of
// the progress gossip cluster, and a rebuilt cluster starts from it.
//
// The pod holds no Kubernetes credential. The operator is the only API
// client, and everything the store records about a Play crosses the bus
// on the topics progressbus.go names.

import (
	"context"
	"errors"
	"net/http"
)

// The two container names a person reads in kubectl logs. They say what
// each container does, and they differ because a pod's container names
// are one set: the agent and the role beside it cannot share one.
const (
	progressContainer = "progress"
	recorderContainer = "recorder"
)

// Where the progress agent writes its database, its write-ahead log,
// and its admin socket, and the configuration it reads. Both are paths
// in the Corrosion image, from corrosion/progress.toml.
const (
	progressStatePath  = "/var/lib/progress"
	progressConfigPath = "/etc/corrosion/progress.toml"
	progressVolumeName = "progress"
)

// The address the progress agent binds and announces. The kubelet
// expands $(POD_IP) from the downward API, so the agent binds the pod's
// own address rather than every address, and Corrosion drops its own
// address from the bootstrap list by comparison with it.
const progressGossipAddress = "$(" + podIPVariable + "):8788"

// The loopback address of the progress agent's HTTP API, and the
// variable that moves it. The role learns it from its environment
// alone, because the pod carries no credential to read it with.
const (
	progressAPIVariable = "LIBRARY_PROGRESS_API"
	defaultProgressAPI  = "http://127.0.0.1:8081"
)

// The pod one Catalog stands for its progress store, and the claim
// under it. Both are named from the Catalog, so every pass names the
// same objects and the operator keeps no record of them.
func progressPodName(catalog string) string {
	return catalog + "-progress"
}

func progressClaimName(catalog string) string {
	return catalog + "-progress"
}

// The label pair the progress pod carries: the name label that tells it
// from a catalog pod and a screen pod, and the progress member label
// the namespace's progress EndpointSlice is written over.
func progressPodLabels() map[string]string {
	return withProgressMemberLabel(map[string]string{scannerLabelKey: progressLabelValue})
}

// The pod the Catalog stands for its progress store. It is a function
// of the Catalog and the operator's own settings alone, so two passes
// over an unchanged Catalog build the same pod, which is what makes the
// template hash mean anything.
func buildProgressPod(catalog *NamespaceCatalog, operatorImage, corrosionImage, busAddress, topicBase, mediaBase string) *Pod {
	grace := int64(scannerGracePeriod)
	// The progress role holds no Kubernetes credential; it reads the
	// bus and writes its own agent, and the operator alone reads the API.
	noToken := false
	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:            progressPodName(catalog.Metadata.Name),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          progressPodLabels(),
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: PodSpec{
			// The progress pod is a standing service and not a run to
			// completion, so the kubelet restarts a container that
			// exits rather than letting the pod end.
			RestartPolicy:                 "Always",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			InitContainers: []Container{
				progressSidecar(corrosionImage),
			},
			Containers: []Container{
				progressRole(catalog, operatorImage, busAddress, topicBase, mediaBase),
			},
			Volumes: []Volume{
				{Name: progressVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: progressClaimName(catalog.Metadata.Name),
				}},
			},
		},
	}
}

// The Corrosion agent of the progress cluster. The image carries the
// configuration and runs the catalog agent by default, so this
// container names the progress configuration in its own arguments.
//
// The agent is a native sidecar: an initContainer with restartPolicy
// Always. The kubelet starts it and waits for its startupProbe before
// it starts the progress role, so the first write never races an API
// that is not listening.
func progressSidecar(image string) Container {
	always := "Always"
	return Container{
		Name:  progressContainer,
		Image: image,
		Args:  []string{"agent", "--config", progressConfigPath},
		Env: []EnvVar{
			{Name: podIPVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: podIPFieldPath},
			}},
			{Name: gossipAddressVariable, Value: progressGossipAddress},
		},
		VolumeMounts: []VolumeMount{
			{Name: progressVolumeName, MountPath: progressStatePath},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": catalogCPURequest, "memory": catalogMemoryRequest},
			Limits:   map[string]string{"memory": catalogMemoryLimit},
		},
		SecurityContext: unprivileged(),
		RestartPolicy:   always,
		// The startupProbe gives a cold agent up to 90 seconds to open
		// its API, and it gates the progress role's start.
		StartupProbe: progressProbe(3, 30),
		// The livenessProbe runs every 30 seconds and restarts a wedged
		// agent after three failures, at near-zero cost.
		LivenessProbe: progressProbe(30, 3),
	}
}

// The probe that runs the agent's query command inside the container on
// the progress configuration. It reaches the API on 127.0.0.1:8081 and
// exits zero only when the API and the database behind it both answer.
func progressProbe(period, failureThreshold int) *Probe {
	return &Probe{
		Exec: &ExecAction{Command: []string{
			catalogBinary, "--config", progressConfigPath, "query", "SELECT 1",
		}},
		PeriodSeconds:    period,
		FailureThreshold: failureThreshold,
	}
}

// The container that reads the bus and writes the progress store. It
// runs this operator's own image in its progress role, and it learns
// the namespace, both topic trees, and its agent's address from its
// environment alone.
func progressRole(catalog *NamespaceCatalog, image, busAddress, topicBase, mediaBase string) Container {
	return Container{
		Name:    recorderContainer,
		Image:   image,
		Command: []string{"/library-operator", progressMode},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: catalog.Metadata.Namespace},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: mediaTopicBaseVariable, Value: mediaBase},
			{Name: progressAPIVariable, Value: defaultProgressAPI},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// The claim the progress pod holds, owned by the Catalog, so the
// garbage collector takes it with the Catalog and the store survives
// every roll of the pod. It takes spec.progress's size and class, and
// each falls back to the catalog's, so a namespace can keep the store
// on a durable class at a size of its own.
//
// A Catalog that names a claim of its own names the catalog's claim,
// never this one, so the operator always provisions the progress claim.
func buildProgressClaim(catalog *NamespaceCatalog) *PersistentVolumeClaim {
	return &PersistentVolumeClaim{
		APIVersion: claimAPIVersion,
		Kind:       "PersistentVolumeClaim",
		Metadata: ObjectMeta{
			Name:            progressClaimName(catalog.Metadata.Name),
			Namespace:       catalog.Metadata.Namespace,
			Labels:          progressPodLabels(),
			OwnerReferences: []OwnerReference{catalogObjectOwner(catalog)},
		},
		Spec: PersistentVolumeClaimSpec{
			AccessModes: []string{accessModeReadWriteOnce},
			Resources: VolumeResourceRequirements{
				Requests: map[string]string{"storage": progressStorageSize(catalog)},
			},
			StorageClassName: progressStorageClass(catalog),
		},
	}
}

// An absent claim is created and an existing one is left alone, the
// rule standCatalogClaim follows, because a claim's spec is immutable
// once it binds. A size a later Catalog grows to reaches a new claim,
// not this one.
func (o *operator) standProgressClaim(ctx context.Context, catalog *NamespaceCatalog) error {
	namespace, name := catalog.Metadata.Namespace, progressClaimName(catalog.Metadata.Name)

	_, err := GetPersistentVolumeClaim(ctx, o.client, namespace, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	_, err = CreatePersistentVolumeClaim(ctx, o.client, buildProgressClaim(catalog))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// The pod that stands for one Catalog's progress store after this pass,
// on the same terms as every other pod this operator stands.
func (o *operator) standProgressPod(ctx context.Context, catalog *NamespaceCatalog) (*Pod, error) {
	if err := o.standProgressClaim(ctx, catalog); err != nil {
		return nil, err
	}
	desired := buildProgressPod(catalog, o.scannerImage, o.corrosionImage,
		o.busAddress, o.topicBase, o.mediaTopicBase)
	return o.standPod(ctx, desired)
}

// The media operator's topic base, as the environment states it, or
// media-operator's own default when it states none.
func mediaTopicBaseOf(stated string) string {
	if stated == "" {
		return defaultMediaTopicBase
	}
	return stated
}

// ProgressMemberQuery narrows a pod list to the pods that hold a
// progress agent. The equals sign inside the selector is
// percent-encoded, so the server reads one parameter and not two.
const progressMemberQuery = "labelSelector=" + progressMemberLabelKey + "%3D" + progressMemberLabelValue

// ListProgressMemberPods reads every pod that holds a progress agent
// across every namespace, because a Catalog is in whatever namespace
// its Libraries are. The pass writes each namespace's progress
// EndpointSlice over the answer.
func ListProgressMemberPods(ctx context.Context, c *Client) (*PodList, error) {
	list := &PodList{}
	if err := c.RequestJSON(ctx, http.MethodGet, podsAllPath+"?"+progressMemberQuery, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}
