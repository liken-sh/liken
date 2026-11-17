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
	"fmt"
	"strconv"
	"time"
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
	// The last run of the placing Job with the model (titlesplace.go).
	Placed titlesPlaced `json:"placed,omitzero"`
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
// broker's fit folded in where it is later than the status's last fit, and
// the broker's placing where it is later than the status's last placing. A
// Library that turned the fact off keeps no entry.
func titlesModelsStatus(library *Library, fit *titlesFit, placing *titlesPlacing) []titlesModelStatus {
	if !library.Spec.Titles.Enabled {
		return nil
	}
	held := endTitlesModel(library.Status.TitlesModels)
	var entry titlesModelStatus
	if held != nil {
		entry = *held
	}
	if fit != nil && fit.Kind == titlesKindEnd && (held == nil || fit.At.After(held.LastFit)) {
		entry = entry.after(*fit)
	}
	if placing != nil && placing.Kind == titlesKindEnd && placing.At.After(entry.Placed.At) {
		entry.Kind = titlesKindEnd
		entry.Placed = placing.titlesPlaced
	}
	if entry.Kind == "" {
		return nil
	}
	return []titlesModelStatus{entry}
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

// The fit step of one Library's pass. It deletes a fit that runs for a
// Library that turned the fact off, and starts a fit where one is due and
// none runs.
func (o *operator) runTitlesFit(ctx context.Context, library *Library, report *libraryReport, jobs []Job,
	now time.Time) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if !library.Spec.Titles.Enabled {
		return o.stopTitlesJobs(ctx, library, jobs, titlesFitMode, "the titles fit")
	}
	if report == nil || titlesJobRuns(library, jobs, titlesFitMode) {
		return nil
	}
	marked := report.TitlesMarked
	baseline := titlesFitBaseline(library, o.titlesFits.latestFor(namespace, name), jobs)
	if !titlesFitDue(marked, baseline) {
		return nil
	}
	cause := fmt.Sprintf("%s of %d films and series have a reading and a provider's mark",
		counted(marked.Videos, "video"), marked.Groups)
	if baseline > 0 {
		cause += fmt.Sprintf(", and the last fit had %d", baseline)
	}
	job := buildTitlesFitJob(library, o.jobImages(), o.jobBus(), marked, now)
	_, err := o.startTitlesJob(ctx, library, job, "to fit the titles model", "which fits the titles model", cause)
	return err
}

// The fit Job of one Library. The pod holds no catalog: it reads the
// readings and the marks from the volume, and the labels file that the run
// writes for the tool goes in the scratch emptyDir.
func buildTitlesFitJob(library *Library, images jobImages, bus busEndpoint, marked titlesMarked,
	created time.Time) *Job {
	return buildTitlesJob(library, images, titlesJobSpec{
		mode:        titlesFitMode,
		timeout:     titlesFitTimeout,
		annotations: map[string]string{titlesMarkedAnnotation: strconv.Itoa(marked.Videos)},
		scratch:     titlesFitScratch,
		env:         bus.env(),
		resources: ResourceRequirements{
			Requests: map[string]string{"cpu": titlesFitCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": titlesFitMemoryLimit},
		},
	}, created)
}
