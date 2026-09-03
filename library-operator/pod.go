package main

// The containers every pod this operator builds is made of, and
// the pod template a scan Job runs. A worker pod holds two containers:
// the worker itself, and a Corrosion agent of its own as a native
// sidecar. They share the pod because they share a loopback address and
// a lifetime: no agent answers on the network, so a worker that writes
// the catalog carries the agent that holds it.

// The containers, and the pod-local names of the two volumes they
// mount. The container names reach a person through kubectl logs, so
// they say what the container does rather than what it runs.

import "encoding/json"

const (
	scannerContainer  = "scanner"
	catalogContainer  = "catalog"
	cleanupContainer  = "cleanup"
	reporterContainer = "reporter"

	libraryVolumeName = "library"
	catalogVolumeName = "catalog"
)

// CatalogStatePath is where the catalog agent writes its database, its
// write-ahead log, and its admin socket. The image's own configuration
// names this one directory, so the durable catalog claim mounted here is
// every writable path the agent needs.
const catalogStatePath = "/var/lib/corrosion"

// The database file the agent writes under that directory, from
// corrosion/config.toml. The media browser reads the catalog straight from
// this file, so the name is stated here and in the image's configuration and
// nowhere else.
const catalogStateFile = "state.db"

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

// CatalogBinary is the Corrosion binary the image's entrypoint
// runs, from corrosion/Dockerfile. The kubelet's probes run it with the
// query subcommand, which reaches the agent's loopback API from inside
// the container.
const catalogBinary = "/corrosion"

// ScannerGracePeriod is how long the kubelet waits between the SIGTERM
// and the kill. A busy catalog agent flushes its database on the way
// out, so a pod asks for a minute rather than the default 30 seconds.
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

// The pod a scan Job runs: the scanner beside a Corrosion agent
// on the Library's own catalog claim, with the library volume mounted
// read-only. It is a function of the Library, the scan path, and the
// operator's own settings alone, so two passes build the same template,
// which is what makes the template hash mean anything.
func scanPodTemplate(library *Library, scanPath, scannerImage, corrosionImage, busAddress, topicBase string) PodTemplateSpec {
	template := workerPodTemplate(library, workerScan,
		scannerSidecar(library, scanPath, scannerImage, busAddress, topicBase), corrosionImage)
	// The library volume is the scanner's alone; the cleanup worker
	// reads no media, so it mounts none.
	template.Spec.Volumes = append(template.Spec.Volumes, Volume{
		Name: libraryVolumeName,
		PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
			ClaimName: library.Spec.Storage.Claim,
			ReadOnly:  true,
		},
	})
	return template
}

// The pod shape both workers share: one worker container, the
// catalog agent beside it on the Library's catalog claim, and no
// Kubernetes credential.
func workerPodTemplate(library *Library, worker string, container Container, corrosionImage string) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	// A worker holds no Kubernetes credential: it writes the catalog
	// through the agent beside it, and the operator alone writes the
	// status. Without this the kubelet would mount the namespace's
	// default ServiceAccount token into both containers.
	noToken := false
	return PodTemplateSpec{
		Metadata: ObjectMeta{
			Labels: withMemberLabel(workerLabels(library.Metadata.Name, worker)),
		},
		Spec: PodSpec{
			// Never, because a Job's pod runs to completion, and a
			// restart in place would hide the failure the Job reports.
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: &grace,
			AutomountServiceAccountToken:  &noToken,
			InitContainers: []Container{
				catalogSidecar(corrosionImage),
			},
			Containers: []Container{container},
			Volumes: []Volume{
				// The agent's state is the Library's own durable claim.
				// It keeps the agent's actor id and its rows between
				// runs, so a run syncs a delta rather than the whole
				// namespace, and its ReadWriteOnce is what serializes
				// one library's workers.
				{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: scannerCatalogClaimName(library.Metadata.Name),
				}},
			},
		},
	}
}

// LibraryOwner ties the pod's life to the Library's. Controller is
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

// ScannerSidecar builds the container that walks the volume. It runs
// this operator's own image in its scan role, unless the kind's
// settings block names an image of its own, which is how a person
// supplies a scanner the project does not ship.
//
// The container learns which Library it serves from its environment
// alone, because it holds no API credential to look one up with. The
// claim is mounted read-only, so a scanner cannot write to the media
// volume whatever it does.
//
// An empty scan path is a full walk, and a path names the one
// folder to rescan; the Job's own name arrives through the downward
// API, because the scanner writes it into the runs row the reporter
// echoes back.
func scannerSidecar(library *Library, scanPath, image, busAddress, topicBase string) Container {
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
			{Name: libraryIgnoreVariable, Value: ignoreValue(library)},
			{Name: scanPathVariable, Value: scanPath},
			{Name: jobNameVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
			}},
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

// CatalogSidecar builds the Corrosion agent. The image carries the
// agent's configuration and runs it as its default command, so the pod
// states only what the image cannot know: the address the agent
// announces, and the directory it writes.
//
// The agent is a native sidecar: an initContainer with
// restartPolicy Always. The kubelet starts it and waits for its
// startupProbe before it starts the scanner, so the scanner's first walk
// never races a catalog API that is not listening.
//
// The probes run a query inside the container, not an httpGet or
// a TCP dial from the kubelet. The agent's API binds loopback alone (see
// corrosion/config.toml), so nothing the kubelet reaches over the pod
// network can dial it. `corrosion query "SELECT 1"` connects to that
// loopback API from inside the container and exits zero only when the
// API answers, which is more than a bound port: it is the API and the
// database behind it both up.
func catalogSidecar(image string) Container {
	always := "Always"
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
		RestartPolicy:   always,
		// The startupProbe gives a cold agent up to 90 seconds to
		// open its API, because an agent that replays its database on
		// start takes a while, and it gates the scanner's start.
		StartupProbe: catalogProbe(3, 30),
		// The livenessProbe runs every 30 seconds and restarts a
		// wedged agent after three failures, at near-zero cost.
		LivenessProbe: catalogProbe(30, 3),
	}
}

// CatalogProbe builds a probe that runs the catalog agent's query
// command inside the container on the given schedule. The query reaches
// the agent's loopback API and exits zero only when it answers.
func catalogProbe(period, failureThreshold int) *Probe {
	return &Probe{
		Exec:             &ExecAction{Command: []string{catalogBinary, "query", "SELECT 1"}},
		PeriodSeconds:    period,
		FailureThreshold: failureThreshold,
	}
}

// Unprivileged is the security context both containers carry. One
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

// The ignore list travels as one JSON value, so a folder name of any
// character reaches the scanner whole.
func ignoreValue(library *Library) string {
	ignore, _ := json.Marshal(library.Spec.Ignore)
	return string(ignore)
}
