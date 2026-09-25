package main

// enrichjob.go builds the one Job a Library runs. The Job runs the walk in
// walk mode, and every phase the Library's sources serve, as regular
// containers that start together, beside one Corrosion agent on the
// Library's one catalog claim. The agent is the only init container, because
// Kubernetes runs a native sidecar as an init container. A close container
// writes the run's start, waits for every phase's mark, and hands off.
//
// The pod names every phase and the facts each runs, so a person reads the
// Job's work with kubectl get pod, and the operator holds no order of its
// own.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The two modes of a library Job. A walk runs the scan container and every
// phase the sources serve. A gap Job runs no scan and only the phases whose
// gaps the last report counted. The mode is the Job's worker label, so a
// person lists the walks of a Library with one selector.
const (
	jobModeWalk = "walk"
	jobModeGaps = "gaps"
)

// The annotation a Job carries with the time the operator created it, in
// RFC 3339. The scheduler reads the newest walk and the newest failure off
// it, because a report can reach the operator after the Job it describes
// has finished.
const jobCreatedAnnotation = "library.liken.sh/created"

// The annotation a walk of folders carries with the folders, as the JSON
// list SCAN_PATHS holds. A walk with none is a full walk, and the scheduler
// reads the last full walk's start off it.
const jobPathsAnnotation = "library.liken.sh/paths"

// What one library Job covers: its mode, the folders a walk rescans and none
// for a full walk, the phases it runs in the table's order, and the write
// its copy of the catalog must hold before the phases read a gap.
type libraryJob struct {
	mode   string
	paths  []string
	phases []servedPhase
	sync   syncTarget
}

// The images a library Job runs: the operator's own, the one with ffmpeg for
// the phases that open a media file, and the catalog agent's.
type jobImages struct {
	operator  string
	ffmpeg    string
	corrosion string
}

// The Job's name: the Library, the mode, and the creation time in base 36,
// so a person reads which kind of run it is and no two Jobs share a name.
func libraryJobName(library, mode string, created time.Time) string {
	return library + "-" + mode + "-" + strconv.FormatInt(created.UnixNano(), 36)
}

// The library Job, owned by the Library so the garbage collector takes it
// with the Library.
func buildLibraryJob(library *Library, providers providerSet, languages []string,
	plan libraryJob, images jobImages, created time.Time) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	labels := workerLabels(library.Metadata.Name, plan.mode)
	annotations := map[string]string{jobCreatedAnnotation: created.UTC().Format(time.RFC3339Nano)}
	if len(plan.paths) > 0 {
		annotations[jobPathsAnnotation] = scanPathsValue(plan.paths)
	}
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            libraryJobName(library.Metadata.Name, plan.mode, created),
			Namespace:       library.Metadata.Namespace,
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template:                libraryPodTemplate(library, providers, languages, plan, images),
		},
	}
}

// The names of the containers the close container waits for: the scan in
// walk mode, and every phase.
func (plan libraryJob) included() []string {
	var names []string
	if plan.mode == jobModeWalk {
		names = append(names, scanPhase)
	}
	for _, phase := range plan.phases {
		names = append(names, phase.name)
	}
	return names
}

// The pod the library Job runs.
func libraryPodTemplate(library *Library, providers providerSet, languages []string,
	plan libraryJob, images jobImages) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	// No container holds a Kubernetes credential. Each reads its work through
	// the agent beside it and takes a provider key through a secretKeyRef, so
	// nothing in this pod reads the API server.
	noToken := false

	included := plan.included()
	var containers []Container
	if plan.mode == jobModeWalk {
		containers = append(containers, walkContainer(library, plan.paths, images.operator))
	}
	for _, phase := range plan.phases {
		containers = append(containers, phaseContainer(library, providers, languages, plan, phase,
			phaseNeedsOf(phase.name, included), images))
	}
	closing := enrichContainer(library, closeMode, closeMode, plan.paths, images.operator)
	withPhaseEnv(&closing, plan, included)
	containers = append(containers, closing)

	spec := PodSpec{
		RestartPolicy:                 "Never",
		TerminationGracePeriodSeconds: &grace,
		AutomountServiceAccountToken:  &noToken,
		InitContainers:                []Container{catalogSidecar(images.corrosion)},
		Containers:                    containers,
		Volumes:                       libraryJobVolumes(library),
	}
	// The pod holds the render claim only where the Library names a render
	// block and the Job runs trickplay. With none it decodes in software.
	if library.Spec.Trickplay.Render != nil && slices.Contains(included, trickplayContainerName) {
		spec.ResourceClaims = []PodResourceClaim{{
			Name:                      renderRequestName,
			ResourceClaimTemplateName: trickplayTemplateName(library.Metadata.Name),
		}}
		for index := range spec.Containers {
			if spec.Containers[index].Name == trickplayContainerName {
				spec.Containers[index].Resources.Claims = []ResourceClaim{{Name: renderRequestName}}
			}
		}
	}
	return PodTemplateSpec{
		Metadata: ObjectMeta{Labels: withMemberLabel(workerLabels(library.Metadata.Name, plan.mode))},
		Spec:     spec,
	}
}

// The volumes of the pod. The storage claim is mounted read-write at the
// volume, and the scan container mounts it read-only, so a scanner a person
// supplies never writes the media. The phases write beside the media, into
// the claim a screen reads: the storage claim, or a franchises library's art
// claim, which the scan also writes the downloaded art into.
func libraryJobVolumes(library *Library) []Volume {
	volumes := []Volume{
		// The agent's state is the Library's own durable claim. It keeps the
		// agent's actor id and its rows between runs, so a run syncs a delta
		// rather than the whole namespace.
		{Name: catalogVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
			ClaimName: scannerCatalogClaimName(library.Metadata.Name),
		}},
		{Name: libraryVolumeName, PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
			ClaimName: library.Spec.Storage.Claim,
		}},
		// The marks and the .nfo locks. An emptyDir is local to the node, so
		// flock reaches every container, and a retried pod starts clean.
		{Name: phasesVolumeName, EmptyDir: &EmptyDirVolumeSource{}},
	}
	if claim := library.Spec.artClaim(); claim != "" {
		volumes = append(volumes, Volume{
			Name:                  artVolumeName,
			PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: claim},
		})
	}
	return volumes
}

// The volume the phases write into at the library mount: the art claim of a
// franchises library, which holds the files its screen reads, and the
// storage claim of every other kind.
func phaseVolumeOf(library *Library) string {
	if library.Spec.artClaim() != "" {
		return artVolumeName
	}
	return libraryVolumeName
}

// The scan container of a walk: the scanner, with the phases volume where it
// writes its mark.
func walkContainer(library *Library, paths []string, image string) Container {
	scan := scannerSidecar(library, paths, image)
	scan.Env = append(scan.Env, EnvVar{Name: libraryPhasesVariable, Value: phasesMountPath})
	scan.VolumeMounts = append(scan.VolumeMounts, VolumeMount{Name: phasesVolumeName, MountPath: phasesMountPath})
	return scan
}

// One phase's container. The phases that open a media file run on the ffmpeg
// image, and each takes the memory line its work needs.
func phaseContainer(library *Library, providers providerSet, languages []string, plan libraryJob,
	phase servedPhase, needs []string, images jobImages) Container {
	image := images.operator
	if phase.name == factProbe || phase.name == trickplayContainerName || phase.name == trailerFileContainerName {
		image = images.ffmpeg
	}
	container := factsContainer(library, phase.name, phase.served, plan.paths, image)
	switch phase.name {
	case factProbe:
		container.Resources.Limits = map[string]string{"memory": probeMemoryLimit}
	case artContainerName:
		// The art container holds an image while it writes it.
		container.Resources.Limits = map[string]string{"memory": artMemoryLimit}
	case trickplayContainerName:
		// Trickplay decodes a video, where every other container reads rows.
		container.Resources.Requests["cpu"] = trickplayCPURequest
		container.Resources.Limits = map[string]string{"memory": trickplayMemoryLimit}
	case trailerFileContainerName:
		container.Resources.Limits = map[string]string{"memory": trailersMemoryLimit}
	}
	// The source order, the keys, and the languages travel in one
	// environment, so a container asks its providers in the order
	// spec.sources names them and ranks by one list.
	container.Env = append(container.Env, providerEnv(library, providers, languages)...)
	container.Env = append(container.Env, EnvVar{Name: libraryPhaseNeedsVariable, Value: strings.Join(needs, ",")})
	withPhaseEnv(&container, plan, nil)
	return container
}

// What every container but the scan shares: the worker its counts go under,
// the phases volume, and the write its copy must hold. The close container
// also names every phase it waits for.
func withPhaseEnv(container *Container, plan libraryJob, waits []string) {
	container.Env = append(container.Env,
		// Every container of the Job counts under one worker, because the
		// Job writes one runs row, under that worker.
		EnvVar{Name: libraryWorkerVariable, Value: workerEnrich},
		EnvVar{Name: libraryPhasesVariable, Value: phasesMountPath},
		EnvVar{Name: syncActorVariable, Value: plan.sync.actor},
		EnvVar{Name: syncVersionVariable, Value: strconv.FormatInt(plan.sync.version, 10)},
	)
	if waits != nil {
		container.Env = append(container.Env, EnvVar{Name: libraryPhaseNeedsVariable, Value: strings.Join(waits, ",")})
	}
	container.VolumeMounts = append(container.VolumeMounts,
		VolumeMount{Name: phasesVolumeName, MountPath: phasesMountPath})
}

// One container of the operator's image. Its name is the phase and its
// command is the role, and it learns everything else from the environment,
// because it holds no credential to look a Library up with. The kind's own
// image is the scanner's alone: a scanner a person supplies runs no phase.
func enrichContainer(library *Library, name, role string, paths []string, image string) Container {
	return Container{
		Name:    name,
		Image:   image,
		Command: []string{"/library-operator", role},
		Env: []EnvVar{
			{Name: libraryContainerVariable, Value: name},
			{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
			{Name: libraryNameVariable, Value: library.Metadata.Name},
			{Name: libraryKindVariable, Value: library.Spec.Kind},
			{Name: libraryRootVariable, Value: library.Spec.Storage.Root},
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
			{Name: libraryIgnoreVariable, Value: ignoreValue(library)},
			{Name: libraryRefreshVariable, Value: refreshValue(library)},
			{Name: scanPathsVariable, Value: scanPathsValue(paths)},
			{Name: handoffTimeoutVariable, Value: defaultHandoffTimeout.String()},
			{Name: syncTimeoutVariable, Value: defaultSyncTimeout.String()},
			{Name: jobNameVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
			}},
		},
		VolumeMounts: []VolumeMount{
			{Name: phaseVolumeOf(library), MountPath: libraryMountPath},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// A container that runs facts. Its name is the phase, and LIBRARY_FACTS names
// the facts it runs in order, so the pod reads as the work and one container
// fills more than one gap.
func factsContainer(library *Library, name string, facts []string, paths []string, image string) Container {
	container := enrichContainer(library, name, factsMode, paths, image)
	container.Env = append(container.Env,
		EnvVar{Name: libraryFactsVariable, Value: strings.Join(facts, ",")})
	return container
}

// The refresh times travel as one JSON value, the way the ignore list
// does, so a fact of any name reaches the container whole. A Library
// that names none writes an empty value. The walk is not a fact and no
// container runs it, so it does not travel.
func refreshValue(library *Library) string {
	facts := refreshTimes{}
	for name, at := range library.Spec.Refresh {
		if isContainerFact(name) {
			facts[name] = at
		}
	}
	if len(facts) == 0 {
		return ""
	}
	refresh, _ := json.Marshal(facts)
	return string(refresh)
}

// ffprobe is a child process of the probe container, so its memory counts
// against the container's limit. It holds about 60 MB on its own while it
// reads one file, whatever the file's size, which is more than the scanner's
// limit allows.
const probeMemoryLimit = "256Mi"
