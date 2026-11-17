package main

// titlesjob.go holds what the two Jobs of the titles model share: the fit
// Job, which fits the model (titlesfitjob.go), and the placing Job, which
// places the end titles of every reading with it (titlesplacejob.go). Each
// Job is one pod on the titles image for one Library. Each publishes its
// result retained on the bus, and the operator folds the result into the
// Library's status. So the builder of the Job, the desk of the results, the
// publish of a result, the start of a Job, and the stop of a Job whose
// Library turned the fact off are here once, and each Job states only what
// is its own.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The last result of one Job of the titles model for every Library, as the
// operator's subscription delivers each retained result.
type titlesResults[T comparable] struct {
	mutex  sync.Mutex
	latest map[string]T
	wake   chan<- struct{}
}

func newTitlesResults[T comparable](wake chan<- struct{}) *titlesResults[T] {
	return &titlesResults[T]{latest: map[string]T{}, wake: wake}
}

// Folds one result, and wakes the loop where it is new, because the pass
// that answers it writes it into the Library's status.
func (d *titlesResults[T]) fold(namespace, name string, result T) {
	key := libraryKey(namespace, name)
	d.mutex.Lock()
	held, had := d.latest[key]
	d.latest[key] = result
	d.mutex.Unlock()
	if !had || held != result {
		poke(d.wake)
	}
}

// The last result of one Library, or nil where the broker holds none.
func (d *titlesResults[T]) latestFor(namespace, name string) *T {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	result, held := d.latest[libraryKey(namespace, name)]
	if !held {
		return nil
	}
	return &result
}

// Forgets the result of one Library, and wakes the loop where it held one.
func (d *titlesResults[T]) forget(namespace, name string) {
	key := libraryKey(namespace, name)
	d.mutex.Lock()
	_, had := d.latest[key]
	delete(d.latest, key)
	d.mutex.Unlock()
	if had {
		poke(d.wake)
	}
}

// Reads one retained result off the bus into its desk. An empty payload is a
// cleared topic, which the operator publishes for a Library that is gone, so
// the desk forgets that Library's result. A payload that is not a result is
// reported and dropped, so the desk keeps the last good one.
func foldTitlesResult[T comparable](desk *titlesResults[T], topic, namespace, name string, payload []byte) {
	if len(payload) == 0 {
		desk.forget(namespace, name)
		return
	}
	var result T
	if err := json.Unmarshal(payload, &result); err != nil {
		fmt.Fprintf(os.Stderr, "reading the titles result on %s: %v\n", topic, err)
		return
	}
	desk.fold(namespace, name, result)
}

// Publishes one result retained, so an operator that restarts reads the last
// result of each Library back from the broker. The client name is the Job's
// own with a suffix, so the session does not take over another session of
// the same Job.
func publishTitlesResult(ctx context.Context, dial func(context.Context) (net.Conn, error), client, topic string,
	result any) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, workItemTimeout)
	defer cancel()
	session, err := openBusSession(ctx, dial, client)
	if err != nil {
		return err
	}
	defer session.close()
	if err := session.retain(topic, payload); err != nil {
		return err
	}
	return session.flush()
}

// What one Job of the titles model states of its own: the role, how long it
// may run, what it asks for, the annotations the trigger reads back, the
// directory of its scratch emptyDir, and the environment beside the
// Library's. A Job that writes the catalog also names the catalog agent,
// which runs as a native sidecar on the Library's catalog claim.
type titlesJobSpec struct {
	mode        string
	timeout     time.Duration
	resources   ResourceRequirements
	annotations map[string]string
	scratch     string
	env         []EnvVar
	// Whether the pod mounts the library volume read-only. The fit writes the
	// model there, and the placing Job writes nothing there.
	readOnly bool
	// The catalog agent, for a Job that reads and writes the catalog, and
	// nil for a Job that reads the volume alone.
	agent *Container
}

// One Job of the titles model, owned by the Library so the garbage collector
// takes it with the Library. It is one pod on the titles image, on the volume
// the phases write, and an emptyDir for the files the run writes for the
// tool. The pod holds no Kubernetes credential: it publishes its result on
// the bus.
func buildTitlesJob(library *Library, images jobImages, spec titlesJobSpec, created time.Time) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	deadline := int64((spec.timeout + time.Hour) / time.Second)
	grace := int64(scannerGracePeriod)
	noToken := false
	labels := workerLabels(library.Metadata.Name, spec.mode)
	annotations := map[string]string{jobCreatedAnnotation: created.UTC().Format(time.RFC3339Nano)}
	for key, value := range spec.annotations {
		annotations[key] = value
	}
	env := []EnvVar{
		{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
		{Name: libraryNameVariable, Value: library.Metadata.Name},
		{Name: libraryKindVariable, Value: library.Spec.Kind},
		{Name: libraryRootVariable, Value: library.Spec.Storage.Root},
		{Name: jobNameVariable, ValueFrom: &EnvVarSource{
			FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
		}},
	}
	volume := factWorkerVolume(library)
	volume.PersistentVolumeClaim.ReadOnly = spec.readOnly
	pod := PodSpec{
		RestartPolicy:                 "Never",
		TerminationGracePeriodSeconds: &grace,
		AutomountServiceAccountToken:  &noToken,
		Volumes:                       []Volume{volume, {Name: scratchVolumeName, EmptyDir: &EmptyDirVolumeSource{}}},
		Containers: []Container{{
			Name:    spec.mode,
			Image:   images.titles,
			Command: []string{podBinary, spec.mode},
			Env:     append(env, spec.env...),
			VolumeMounts: []VolumeMount{
				{Name: phaseVolumeOf(library), MountPath: libraryMountPath, ReadOnly: spec.readOnly},
				{Name: scratchVolumeName, MountPath: spec.scratch}},
			Resources:       spec.resources,
			SecurityContext: unprivileged(),
		}},
	}
	podLabels := labels
	if spec.agent != nil {
		// The agent's state is the Library's own catalog claim, which every
		// library Job's agent uses, so the pass starts this Job only while no
		// other Job of the Library that holds the claim is unfinished. The
		// member label puts the pod in the namespace's EndpointSlice, where
		// the agent finds its peers.
		pod.InitContainers = []Container{*spec.agent}
		pod.Volumes = append(pod.Volumes, Volume{Name: catalogVolumeName,
			PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
				ClaimName: scannerCatalogClaimName(library.Metadata.Name),
			}})
		podLabels = withMemberLabel(labels)
	}
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            libraryJobName(library.Metadata.Name, spec.mode, created),
			Namespace:       library.Metadata.Namespace,
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template:                PodTemplateSpec{Metadata: ObjectMeta{Labels: podLabels}, Spec: pod},
		},
	}
}

// Whether a Job of the Library in this role has not finished.
func titlesJobRuns(library *Library, jobs []Job, mode string) bool {
	for _, job := range jobsOf(jobs, library.Metadata.Namespace, library.Metadata.Name, mode) {
		if !job.finished() {
			return true
		}
	}
	return false
}

// Deletes every unfinished Job of the Library in this role, for a Library
// that turned the titles fact off. A finished Job stays for a person to
// read, and goes as every finished Job goes.
func (o *operator) stopTitlesJobs(ctx context.Context, library *Library, jobs []Job, mode, named string) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	for _, job := range jobsOf(jobs, namespace, name, mode) {
		if job.finished() {
			continue
		}
		if err := o.deleteJob(ctx, namespace, job.Metadata.Name); err != nil {
			return fmt.Errorf("deleting %s %s: %w", named, job.Metadata.Name, err)
		}
		o.logf("library %s/%s: deleted %s %s, because the Library turned titles off",
			namespace, name, named, job.Metadata.Name)
	}
	return nil
}

// Creates one Job of the titles model, and says why in the log and in an
// Event on the Library. purpose completes "created the job X ..." and
// described completes "created the Job X, ...". A conflict is another
// writer's create of the same Job, and that writer's line says so. It
// answers whether this call created the Job.
func (o *operator) startTitlesJob(ctx context.Context, library *Library, job *Job, purpose, described,
	cause string) (bool, error) {
	_, err := o.createJob(ctx, job)
	if errors.Is(err, apiclient.ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("creating the Job %s: %w", job.Metadata.Name, err)
	}
	o.logf("library %s/%s: created the job %s %s, because %s", library.Metadata.Namespace, library.Metadata.Name,
		job.Metadata.Name, purpose, cause)
	o.recorder.Normal(libraryReference(library), reasonJobCreated,
		fmt.Sprintf("created the Job %s, %s, because %s", job.Metadata.Name, described, cause))
	return true, nil
}
