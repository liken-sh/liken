package main

// The scanner pod is what a Library becomes at run time: one pod per
// Library, in the Library's namespace, owned by it. Deleting the
// Library is the whole teardown, because the garbage collector takes
// an owned pod with its owner.
//
// The pod holds two containers. The scanner walks the volume and
// reports what it holds over the bus, and the catalog agent holds the
// catalog the scanner writes and replicates it to the pods that read
// it. They share the pod because they share a loopback address and a
// lifetime: the catalog an agent holds describes the volume its own
// scanner walks.

// The two containers, and the pod-local names of the two volumes they
// mount. The container names reach a person through kubectl logs, so
// they say what the container does rather than what it runs.
const (
	scannerContainer = "scanner"
	catalogContainer = "catalog"

	libraryVolumeName = "library"
	catalogVolumeName = "catalog"
)

// catalogStatePath is where the catalog agent writes its database, its
// write-ahead log, and its admin socket. The image's own configuration
// names this one directory, so an emptyDir here is every writable path
// the agent needs.
const catalogStatePath = "/var/lib/corrosion"

// The two variables the catalog agent reads. Corrosion takes an
// environment variable over the matching setting in its configuration
// file, with two underscores between the table and the key, so
// GOSSIP__ADDR is the gossip table's bind address.
//
// The image is built long before any pod exists, so its configuration
// cannot name the pod's address. The kubelet assigns that address when
// it starts the pod, the downward API reads it into POD_IP, and the
// kubelet expands $(POD_IP) in the value beside it. So the agent binds
// the gossip port on the pod's own address, and it announces the
// address it bound.
//
// The agent binds the pod's address rather than every address on
// purpose. Corrosion drops its own address from the bootstrap list by
// comparison with the address it bound. An agent bound on 0.0.0.0
// finds its own pod in the list, announces to itself on every retry,
// and logs an error each time.
const (
	podIPVariable         = "POD_IP"
	podIPFieldPath        = "status.podIP"
	gossipAddressVariable = "GOSSIP__ADDR"
	gossipAddress         = "$(" + podIPVariable + "):8787"
)

// scannerGracePeriod is how long the kubelet waits between the SIGTERM
// and the kill. A busy catalog agent flushes its database on the way
// out, so the pod asks for a minute rather than the default 30
// seconds.
const scannerGracePeriod = 60

// The room each container asks for. The requests are what the
// scheduler places the pod by, and they are small because both
// containers idle between walks. Only memory is capped: a container
// over its memory limit is killed, which is the failure worth having,
// where a CPU limit only throttles a walk that is already bounded by
// the volume it reads.
//
// The catalog agent's ceiling is the wide one. Its first sync holds
// the whole catalog in memory as it applies it, which measured up to
// 380 MB, and it settles far below that once the sync completes.
const (
	scannerCPURequest    = "10m"
	scannerMemoryRequest = "32Mi"
	scannerMemoryLimit   = "64Mi"

	catalogCPURequest    = "10m"
	catalogMemoryRequest = "64Mi"
	catalogMemoryLimit   = "512Mi"
)

// scannerPodName is the pod one Library becomes. The name is derived
// rather than generated, so every pass names the same pod and the
// operator needs no record of what it created.
func scannerPodName(library string) string {
	return library + "-scanner"
}

// buildScannerPod writes the pod one Library becomes. It is a function
// of the Library and the operator's own settings alone, so two passes
// over an unchanged Library build the same pod, which is what makes
// the template hash mean anything.
func buildScannerPod(library *Library, scannerImage, corrosionImage, busAddress, topicBase string) *Pod {
	grace := int64(scannerGracePeriod)
	// The scanner holds no Kubernetes credential: it reports over the
	// bus, and the operator alone writes the status. Without this the
	// kubelet would mount the namespace's default ServiceAccount token
	// into both containers, a credential nothing in the pod should hold.
	noToken := false
	return &Pod{
		APIVersion: podAPIVersion,
		Kind:       "Pod",
		Metadata: ObjectMeta{
			Name:      scannerPodName(library.Metadata.Name),
			Namespace: library.Metadata.Namespace,
			Labels: map[string]string{
				scannerLabelKey: scannerLabelValue,
				libraryLabelKey: library.Metadata.Name,
			},
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: PodSpec{
			// A scanner is a standing service and not a run to
			// completion, so the kubelet restarts a container that
			// exits rather than letting the pod end.
			RestartPolicy:                 "Always",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			Containers: []Container{
				scannerSidecar(library, scannerImage, busAddress, topicBase),
				catalogSidecar(corrosionImage),
			},
			Volumes: []Volume{
				{
					Name: libraryVolumeName,
					PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
						ClaimName: library.Spec.Storage.Claim,
						ReadOnly:  true,
					},
				},
				{Name: catalogVolumeName, EmptyDir: &EmptyDirVolumeSource{}},
			},
		},
	}
}

// libraryOwner ties the pod's life to the Library's. Controller is
// true because exactly one thing manages this pod, and the UID is what
// the garbage collector matches: a Library deleted and recreated under
// the same name is a different owner, and the old pod goes.
func libraryOwner(library *Library) OwnerReference {
	return OwnerReference{
		APIVersion: libraryAPIVersion,
		Kind:       "Library",
		Name:       library.Metadata.Name,
		UID:        library.Metadata.UID,
		Controller: true,
	}
}

// scannerSidecar builds the container that walks the volume. It runs
// this operator's own image in its scan role, unless the kind's
// settings block names an image of its own, which is how a person
// supplies a scanner the project does not ship.
//
// The container learns which Library it serves from its environment
// alone, because it holds no API credential to look one up with. The
// claim is mounted read-only, so a scanner cannot write to the media
// volume whatever it does.
func scannerSidecar(library *Library, image, busAddress, topicBase string) Container {
	if settings := library.Spec.settings(); settings != nil && settings.Image != "" {
		image = settings.Image
	}
	return Container{
		Name:    scannerContainer,
		Image:   image,
		Command: []string{"/library-operator", scanMode},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
			{Name: libraryNameVariable, Value: library.Metadata.Name},
			{Name: libraryKindVariable, Value: library.Spec.Kind},
			{Name: libraryRootVariable, Value: library.Spec.Storage.Root},
			{Name: busAddressVariable, Value: busAddress},
			{Name: topicBaseVariable, Value: topicBase},
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
		},
		VolumeMounts: []VolumeMount{
			{Name: libraryVolumeName, MountPath: libraryMountPath, ReadOnly: true},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// catalogSidecar builds the Corrosion agent. The image carries the
// agent's configuration and runs it as its default command, so the pod
// states only what the image cannot know: the address the agent
// announces, and the directory it writes.
//
// The container carries no probe. Corrosion answers /v1/health with a
// 503 while it has no peers, and an agent that holds one pod's catalog
// alone is the ordinary state in this plan, so a health probe would
// restart a working agent forever.
func catalogSidecar(image string) Container {
	return Container{
		Name:  catalogContainer,
		Image: image,
		Env: []EnvVar{
			{Name: podIPVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: podIPFieldPath},
			}},
			{Name: gossipAddressVariable, Value: gossipAddress},
		},
		VolumeMounts: []VolumeMount{
			{Name: catalogVolumeName, MountPath: catalogStatePath},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": catalogCPURequest, "memory": catalogMemoryRequest},
			Limits:   map[string]string{"memory": catalogMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// unprivileged is the security context both containers carry. One
// reads a mounted volume and the other writes a database on a
// loopback socket, so neither needs a capability, and neither may
// gain one.
func unprivileged() *SecurityContext {
	escalation := false
	return &SecurityContext{
		Capabilities:             &Capabilities{Drop: []string{"ALL"}},
		AllowPrivilegeEscalation: &escalation,
	}
}
