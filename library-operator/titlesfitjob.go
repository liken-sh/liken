package main

// titlesfitjob.go is the operator's side of the titles model: when it starts
// a Library's fit Job, the Job it starts, and what it writes into the
// Library's status from the result the Job publishes.
//
// The report of a Library carries the count of videos a fit can learn from
// (titlesfit.go), and the fit Job publishes what it fitted on the bus
// (titlesfitrun.go). A pass starts a fit when the count has passed the
// floors, or has grown by half over the count of the last fit, and no fit of
// the Library runs. The last fit's count comes from the broker's retained
// result, and from the Library's status where the broker holds none, so a
// broker that restarted starts no fit that the status already answers.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The annotation a fit Job carries with the count of marked videos it was
// started on, so a pass reads which count a Job that is still listed answers,
// whether or not its result reached the broker.
const titlesMarkedAnnotation = "library.liken.sh/titles-marked"

// What the fit's container asks for and the memory it may take. The fit
// holds every marked second of the Library in memory, so its memory grows
// with the Library. Plan 96 holds the measured run that the limit comes
// from, with room for a library several times its size.
const (
	titlesFitMemoryLimit = "4Gi"
	titlesFitCPURequest  = "1"
)

// The last fit of every Library, as the operator's subscription delivers
// each retained result.
type titlesFits struct {
	mutex  sync.Mutex
	latest map[string]titlesFit
	wake   chan<- struct{}
}

func newTitlesFits(wake chan<- struct{}) *titlesFits {
	return &titlesFits{latest: map[string]titlesFit{}, wake: wake}
}

// Folds one result, and wakes the loop where it is new, because the pass
// that answers it writes it into the Library's status.
func (d *titlesFits) fold(namespace, name string, fit titlesFit) {
	key := libraryKey(namespace, name)
	d.mutex.Lock()
	held, had := d.latest[key]
	d.latest[key] = fit
	d.mutex.Unlock()
	if !had || held != fit {
		poke(d.wake)
	}
}

// The last fit of one Library, or nil where the broker holds none.
func (d *titlesFits) latestFor(namespace, name string) *titlesFit {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	fit, held := d.latest[libraryKey(namespace, name)]
	if !held {
		return nil
	}
	return &fit
}

// The model of one kind of titles as the Library's status holds it: the
// model the Library places with, and the last fit, which is another fit
// where it left no model.
type titlesModelStatus struct {
	// The titles the model places.
	Kind string `json:"kind"`
	// When the model was fitted, and empty where the Library has none.
	Fitted time.Time `json:"fitted,omitzero"`
	// The videos the model was fitted on, the marks the fit left out as
	// wrong, and the videos it was scored on, which it was not fitted on.
	Videos  int `json:"videos"`
	LeftOut int `json:"leftOut"`
	HeldOut int `json:"heldOut"`
	// The percent of the held-out videos the model placed within 3, 10, and
	// 30 seconds of their marks.
	Within3  int `json:"within3"`
	Within10 int `json:"within10"`
	Within30 int `json:"within30"`
	// The last fit: its time, its result, the videos with a reading and a
	// mark it found and their films and series, and why it left no model.
	LastFit      time.Time `json:"lastFit,omitzero"`
	LastResult   string    `json:"lastResult,omitempty"`
	LastReason   string    `json:"lastReason,omitempty"`
	MarkedVideos int       `json:"markedVideos"`
	MarkedGroups int       `json:"markedGroups"`
}

// The model of the end titles in a status, or nil.
func endTitlesModel(models []titlesModelStatus) *titlesModelStatus {
	for at := range models {
		if models[at].Kind == titlesKindEnd {
			return &models[at]
		}
	}
	return nil
}

// The status entry after one more fit. A fitted result replaces the model's
// numbers. A rejected or failed one keeps the model the Library has, because
// the fit left that model's file as it was, and records itself as the last
// fit.
func (s titlesModelStatus) after(fit titlesFit) titlesModelStatus {
	s.Kind = titlesKindEnd
	s.LastFit, s.LastResult, s.LastReason = fit.At, fit.Result, fit.Reason
	s.MarkedVideos, s.MarkedGroups = fit.Marked.Videos, fit.Marked.Groups
	if fit.Result == titlesFitFitted {
		s.Fitted, s.Videos, s.LeftOut, s.HeldOut = fit.At, fit.Videos, fit.LeftOut, fit.HeldOut
		s.Within3, s.Within10, s.Within30 = percent(fit.Within3), percent(fit.Within10), percent(fit.Within30)
	}
	return s
}

func percent(share float64) int {
	return int(share*100 + 0.5)
}

// The titles models of a Library's status: what the status held, with the
// broker's result folded in where it is later than the status's last fit. A
// Library that turned the fact off keeps no entry.
func titlesModelsStatus(library *Library, fit *titlesFit) []titlesModelStatus {
	if !library.Spec.Titles.Enabled {
		return nil
	}
	held := endTitlesModel(library.Status.TitlesModels)
	if fit == nil || fit.Kind != titlesKindEnd || (held != nil && !fit.At.After(held.LastFit)) {
		if held == nil {
			return nil
		}
		return []titlesModelStatus{*held}
	}
	var entry titlesModelStatus
	if held != nil {
		entry = *held
	}
	return []titlesModelStatus{entry.after(*fit)}
}

// The condition of the end titles model, and false where the Library does
// not run the fact, which then carries no such condition. fitting says a fit
// Job of the Library runs.
func endTitlesModelCondition(library *Library, models []titlesModelStatus, marked titlesMarked, fitting bool,
	generation int64) (Condition, bool) {
	if !library.Spec.Titles.Enabled {
		return Condition{}, false
	}
	condition := Condition{Type: conditionEndTitlesModel, Status: ConditionFalse, ObservedGeneration: generation}
	model := endTitlesModel(models)
	switch {
	case model != nil && !model.Fitted.IsZero():
		condition.Status = ConditionTrue
		condition.Reason = reasonModelFitted
		condition.Message = fmt.Sprintf("the model was fitted on %s, and it placed %d%% of the %d videos it was "+
			"not fitted on within 10 seconds of their marks and %d%% within 30",
			counted(model.Videos, "video"), model.Within10, model.HeldOut, model.Within30)
	case fitting:
		condition.Reason = reasonModelFitting
		condition.Message = "the first fit of the model runs"
	case model != nil && model.LastResult == titlesFitRejected:
		condition.Reason = reasonModelRejected
		condition.Message = model.LastReason
	case model != nil && model.LastResult == titlesFitFailed:
		condition.Reason = reasonModelFitFailed
		condition.Message = model.LastReason
	default:
		condition.Reason = reasonWaitingForMarks
		condition.Message = fmt.Sprintf("%s of %d films and series have a reading and a provider's mark, and the "+
			"first fit starts at %d videos of %d", counted(marked.Videos, "video"), marked.Groups,
			titlesFitVideos, titlesFitGroups)
	}
	return condition, true
}

// The count of marked videos the last fit of a Library answers: the greatest
// of the status's last fit, the broker's last result, and the fit Jobs the
// pass still lists. A Job counts whether it finished or not, so a fit whose
// result never reached the broker starts no second fit on the same videos
// while its Job stays.
func titlesFitBaseline(library *Library, fit *titlesFit, jobs []Job) int {
	baseline := 0
	if model := endTitlesModel(library.Status.TitlesModels); model != nil {
		baseline = model.MarkedVideos
	}
	if fit != nil {
		baseline = max(baseline, fit.Marked.Videos)
	}
	for _, job := range jobsOf(jobs, library.Metadata.Namespace, library.Metadata.Name, titlesFitMode) {
		count, _ := strconv.Atoi(job.Metadata.Annotations[titlesMarkedAnnotation])
		baseline = max(baseline, count)
	}
	return baseline
}

// Whether a fit Job of the Library has not finished.
func titlesFitRuns(library *Library, jobs []Job) bool {
	for _, job := range jobsOf(jobs, library.Metadata.Namespace, library.Metadata.Name, titlesFitMode) {
		if !job.finished() {
			return true
		}
	}
	return false
}

// The fit step of one Library's pass. It deletes a fit that runs for a
// Library that turned the fact off, and starts a fit where one is due and
// none runs.
func (o *operator) runTitlesFit(ctx context.Context, library *Library, report *libraryReport, jobs []Job,
	now time.Time) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if !library.Spec.Titles.Enabled {
		for _, job := range jobsOf(jobs, namespace, name, titlesFitMode) {
			if job.finished() {
				continue
			}
			if err := o.deleteJob(ctx, namespace, job.Metadata.Name); err != nil {
				return fmt.Errorf("deleting the titles fit %s: %w", job.Metadata.Name, err)
			}
			o.logf("library %s/%s: deleted the titles fit %s, because the Library turned titles off",
				namespace, name, job.Metadata.Name)
		}
		return nil
	}
	if report == nil || titlesFitRuns(library, jobs) {
		return nil
	}
	marked := report.TitlesMarked
	baseline := titlesFitBaseline(library, o.titlesFits.latestFor(namespace, name), jobs)
	if !titlesFitDue(marked, baseline) {
		return nil
	}
	job := buildTitlesFitJob(library, o.jobImages(), o.jobBus(), marked, now)
	_, err := o.createJob(ctx, job)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("creating the titles fit %s: %w", job.Metadata.Name, err)
	}
	cause := fmt.Sprintf("%s of %d films and series have a reading and a provider's mark",
		counted(marked.Videos, "video"), marked.Groups)
	if baseline > 0 {
		cause += fmt.Sprintf(", and the last fit had %d", baseline)
	}
	o.logf("library %s/%s: created the job %s to fit the titles model, because %s", namespace, name,
		job.Metadata.Name, cause)
	o.recorder.Normal(libraryReference(library), reasonJobCreated,
		fmt.Sprintf("created the Job %s, which fits the titles model, because %s", job.Metadata.Name, cause))
	return nil
}

// The fit Job of one Library, owned by the Library so the garbage collector
// takes it with the Library. It is one pod on the titles image, on the
// volume the phases write. The pod holds no catalog and no Kubernetes
// credential: it reads the readings and the marks from the volume, and
// publishes its result on the bus. An emptyDir holds the labels file that
// the run writes for the tool.
func buildTitlesFitJob(library *Library, images jobImages, bus busEndpoint, marked titlesMarked,
	created time.Time) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	deadline := int64((titlesFitTimeout + time.Hour) / time.Second)
	grace := int64(scannerGracePeriod)
	noToken := false
	labels := workerLabels(library.Metadata.Name, titlesFitMode)
	env := []EnvVar{
		{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
		{Name: libraryNameVariable, Value: library.Metadata.Name},
		{Name: libraryKindVariable, Value: library.Spec.Kind},
		{Name: libraryRootVariable, Value: library.Spec.Storage.Root},
		{Name: jobNameVariable, ValueFrom: &EnvVarSource{
			FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
		}},
	}
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:      libraryJobName(library.Metadata.Name, titlesFitMode, created),
			Namespace: library.Metadata.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				jobCreatedAnnotation:   created.UTC().Format(time.RFC3339Nano),
				titlesMarkedAnnotation: strconv.Itoa(marked.Videos),
			},
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: PodTemplateSpec{
				Metadata: ObjectMeta{Labels: labels},
				Spec: PodSpec{
					RestartPolicy:                 "Never",
					TerminationGracePeriodSeconds: &grace,
					AutomountServiceAccountToken:  &noToken,
					Volumes: []Volume{factWorkerVolume(library),
						{Name: scratchVolumeName, EmptyDir: &EmptyDirVolumeSource{}}},
					Containers: []Container{{
						Name:    titlesFitMode,
						Image:   images.titles,
						Command: []string{podBinary, titlesFitMode},
						Env:     append(env, bus.env()...),
						VolumeMounts: []VolumeMount{
							{Name: phaseVolumeOf(library), MountPath: libraryMountPath},
							{Name: scratchVolumeName, MountPath: titlesFitScratch}},
						Resources: ResourceRequirements{
							Requests: map[string]string{"cpu": titlesFitCPURequest, "memory": scannerMemoryRequest},
							Limits:   map[string]string{"memory": titlesFitMemoryLimit},
						},
						SecurityContext: unprivileged(),
					}},
				},
			},
		},
	}
}
