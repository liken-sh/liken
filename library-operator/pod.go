package main

// The containers every pod this operator builds is made of, and the pod
// template the cleanup Job runs. A worker pod holds the worker's containers
// and a Corrosion agent of its own as a native sidecar. They share the pod
// because they share a loopback address and a lifetime: no agent answers on
// the network, so a worker that writes the catalog carries the agent that
// holds it.

// The containers, and the pod-local names of the two volumes they
// mount. The container names reach a person through kubectl logs, so
// they say what the container does rather than what it runs.

import "encoding/json"

const (
	scannerContainer  = "scan"
	catalogContainer  = "catalog"
	cleanupContainer  = "cleanup"
	reporterContainer = "reporter"
	// The container that confirms a Job's run against one copy.
	confirmerContainer = "confirmer"

	libraryVolumeName = "library"
	catalogVolumeName = "catalog"
	// artVolumeName is the art claim a franchises scan mounts beside its
	// storage claim. The storage holds the checkout and is read-only, so
	// the art the scan downloads lands on a claim of its own.
	artVolumeName = "art"
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

// The port Corrosion's own configuration opens for its Prometheus
// metrics (corrosion/config.toml's [telemetry.prometheus]). It answers
// on the pod network, unlike the write API, so the catalog PodMonitor
// can reach it.
const (
	catalogMetricsPort     = 9090
	catalogMetricsPortName = "corro-metrics"
)

// AgentExitWait is how long, in seconds, a Corrosion agent waits for its
// own tasks after SIGTERM before it exits without them. The value is
// fixed in Corrosion's spawn crate, and no setting changes it.
const agentExitWait = 60

// ScannerGracePeriod is how long the kubelet waits between the SIGTERM
// and the kill on every pod that runs a catalog or progress agent,
// except a screen pod, which has screenGracePeriod. The Jellyfin pods
// use it too. The agent is a native sidecar, so it receives its SIGTERM
// only after the pod's other containers exit, inside the same period.
//
// An agent at rest exits about 5 s after its SIGTERM, most of it spent
// telling its peers that it leaves: a catalog pod on the testbed was
// gone 6.2 s after kubectl delete. Over eight days on a home cluster,
// 799 agents of library Jobs had a median of 5 s and a 95th percentile
// of 10 s.
//
// The slow exits are agents that apply buffered changes, most often on
// a restart in the middle of a first sync. Corrosion checks for SIGTERM
// only between batches of that work. A batch is one version, or, after
// the agent applies a changed schema at start, every fully buffered
// version on the claim. The agent waits for the batch up to
// agentExitWait and then exits without it. One agent needed 36 s for
// one version behind a slow commit. One applied a list for the whole
// 60 s, until its own wait or a grace period of 60 s ended it, and a
// drill of that case exited after 61.3 s.
//
// 90 s covers the agent's own wait, which includes the 5 s it takes to
// leave, the other containers' exit, and a margin for the one version
// that is still being applied when the wait ends. A kill during a sync
// loses no committed row, because SQLite rolls back an unfinished
// transaction and the next start syncs the rest, but it discards the
// work of the transaction it interrupts. The period is a ceiling: a pod
// whose containers exit on SIGTERM ends before it, so the Jellyfin
// pods, which run no agent, share it at no cost.
const scannerGracePeriod = 90

// The room each container asks for. The requests are what the
// scheduler places the pod by, and they are small because both
// containers idle between walks. Only memory is capped: a container
// over its memory limit is killed, which is the failure worth having,
// where a CPU limit only throttles a walk that is already bounded by
// the volume it reads.
//
// The catalog agent's ceiling is the wide one. A first sync onto an
// empty claim receives changesets from its peers faster than SQLite
// applies them, and the agent holds the ones that wait in a queue.
// corrosion/config.toml bounds that queue at 1,000 changesets, about
// 100 MB, and corrosion/Dockerfile lowers the memory glibc keeps after
// SQLite frees it. With both bounds, a first sync of a synthetic
// catalog of 600,000 rows peaked at 338Mi.
//
// The agent of a Library's Job has a higher ceiling than the other
// agents. The catalog claim of a Job is per node, so every node that
// first runs a Job of a Library syncs the whole namespace onto an empty
// claim once. With Corrosion's default queue of 20,000 changesets,
// that first sync on a home cluster filled the queue and exceeded 1Gi.
// A restart continues from the state.db on the claim, but a phase that
// is reading from the agent's API when the agent is killed fails, and
// so does the Job. The request
// stays the same, because the request is what the scheduler places the
// pod by. A Library's Job tolerates no taint, so it does not run on a
// small screen node that carries the playerTaintKey taint.
const (
	scannerCPURequest    = "10m"
	scannerMemoryRequest = "32Mi"
	scannerMemoryLimit   = "64Mi"

	catalogCPURequest    = "10m"
	catalogMemoryRequest = "64Mi"
	catalogMemoryLimit   = "512Mi"

	libraryJobAgentMemoryLimit = "1Gi"
)

// The pod shape of a worker with one container: the container, the
// catalog agent beside it on the Library's catalog claim, and no
// Kubernetes credential. The cleanup Job runs it.
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
				libraryJobAgent(corrosionImage),
			},
			Containers: []Container{container},
			Volumes: []Volume{
				// The agent's state is the Library's own durable claim.
				// It keeps the agent's actor id and its rows between
				// runs, so a run syncs a delta rather than the whole
				// namespace. The operator starts this pod only when no
				// other Job of the Library is unfinished.
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
// No folder is a full walk, and a list names the folders to rescan. The
// Job's own name arrives through the downward API, because the scanner
// writes it into the runs row.
func scannerSidecar(library *Library, paths []string, image string) Container {
	single := ""
	if len(paths) == 1 {
		single = paths[0]
	}
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
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
			{Name: libraryIgnoreVariable, Value: ignoreValue(library)},
			{Name: libraryArtVariable, Value: artPathOf(library)},
			{Name: scanPathVariable, Value: single},
			{Name: scanPathsVariable, Value: scanPathsValue(paths)},
			{Name: jobNameVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
			}},
		},
		VolumeMounts: scannerMounts(library),
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
// startupProbe before it starts the Job's other containers, so no
// container's first read or write races a catalog API that is not
// listening.
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
		Ports: []ContainerPort{
			{Name: catalogMetricsPortName, ContainerPort: catalogMetricsPort},
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

// libraryJobAgent is the catalog agent of a Library's Job: the same
// native sidecar, with the higher memory limit a first sync on an
// empty claim needs.
func libraryJobAgent(image string) Container {
	agent := catalogSidecar(image)
	agent.Resources.Limits = map[string]string{"memory": libraryJobAgentMemoryLimit}
	return agent
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

// scannerMounts are the storage claim every scanner mounts read-only, and
// the art claim a franchises scanner mounts writable beside it.
func scannerMounts(library *Library) []VolumeMount {
	mounts := []VolumeMount{
		{Name: libraryVolumeName, MountPath: libraryMountPath, ReadOnly: true},
	}
	if library.Spec.artClaim() != "" {
		mounts = append(mounts, VolumeMount{Name: artVolumeName, MountPath: artMountPath})
	}
	return mounts
}

// artPathOf is where the art claim is mounted. The scanner learns it from
// its environment alone, because the pod carries no credential to read
// the Library with. It is empty for a library that names no art claim, and
// that scanner downloads nothing.
func artPathOf(library *Library) string {
	if library.Spec.artClaim() == "" {
		return ""
	}
	return artMountPath
}

// The ignore list travels as one JSON value, so a folder name of any
// character reaches the scanner whole.
func ignoreValue(library *Library) string {
	ignore, _ := json.Marshal(library.Spec.Ignore)
	return string(ignore)
}
