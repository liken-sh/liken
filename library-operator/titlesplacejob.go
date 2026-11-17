package main

// titlesplacejob.go is the operator's side of the placing of the end titles:
// when a pass starts a Library's placing Job, and the Job it starts. The
// trigger is in titlesplace.go, and the pod's program in titlesplacerun.go.
//
// The placing Job writes the catalog, so it runs the catalog agent of a
// library Job on the Library's catalog claim. Two agents on one claim
// corrupt its database, so the Job starts only while no other Job of the
// Library that holds the claim is unfinished, and a library Job waits for
// it in the same way (libraryschedule.go).

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// The role a placing Job's container runs.
const titlesPlaceMode = "titles-place"

// The variable that gives the run the time the Library's model was fitted.
// The model file states no time, and the run holds no credential to read the
// Library's status, so the operator passes the time it compares runs by, and
// the run gives it back in its result.
const titlesModelFittedVariable = "TITLES_MODEL_FITTED"

// The emptyDir the run writes the list of videos in. The image is a closure
// on scratch and holds no temporary directory, and the volume is mounted
// read-only.
const titlesPlaceScratch = "/var/cache/titles-place"

// How long `titles place` may run. Plan 96 measured a run over a whole
// library in seconds, so the timeout bounds only a run that hangs.
const titlesPlaceTimeout = time.Hour

// The threads `titles place` works out the readings' signals on. Plan 96
// holds the measurement on one thread and on four. The container states no
// CPU limit, so the count sets how much of a node one run takes.
const titlesPlaceThreads = 4

// What the placing container asks for and the memory it may take. `place`
// keeps the time and the probability of each second of every reading, 12
// bytes a second, so 8,000 readings of 15 minutes keep about 90 MB. The limit
// leaves room for the model and for the readings that the threads parse at
// once. Nobody has measured the peak of a run.
const (
	titlesPlaceCPURequest  = "1"
	titlesPlaceMemoryLimit = "1Gi"
)

// The placing step of one Library's pass. It deletes a run for a Library
// that turned the fact off, and starts a run where one is due, answering the
// Job it created so the steps after it in the pass see it. A run waits for
// every other Job that holds the catalog claim, and for a fit, which can
// write a new model while the run reads the old one.
func (o *operator) runTitlesPlace(ctx context.Context, library *Library, report *libraryReport, jobs []Job,
	now time.Time) (*Job, error) {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if !library.Spec.Titles.Enabled {
		return nil, o.stopTitlesJobs(ctx, library, jobs, titlesPlaceMode, "the placing of the end titles")
	}
	if report == nil || libraryJobUnfinished(jobs, namespace, name) || titlesJobRuns(library, jobs, titlesFitMode) {
		return nil, nil
	}
	model := endTitlesModel(titlesModelsStatus(library, o.titlesFits.latestFor(namespace, name),
		o.titlesPlacings.latestFor(namespace, name)))
	if model == nil || model.Fitted.IsZero() {
		return nil, nil
	}
	placings := jobsOf(jobs, namespace, name, titlesPlaceMode)
	last := titlesPlaceBaseline(model, o.titlesPlacings.latestFor(namespace, name), placings)
	cause := titlesPlaceCause(model.Fitted, report.TitlesReadings, last, len(placings) > 0,
		titlesJobRuns(library, jobs, factTitles))
	if cause == "" {
		return nil, nil
	}
	job := buildTitlesPlaceJob(library, o.jobImages(), o.jobBus(), o.subtitleLanguages, syncTargetFor(report.Runs),
		model.Fitted, report.TitlesReadings, now)
	created, err := o.startTitlesJob(ctx, library, job, "to place the end titles",
		"which places the end titles", cause)
	if !created {
		return nil, err
	}
	return job, nil
}

// The placing Job of one Library. The pod reads the readings, the names, and
// the model from the volume, which it mounts read-only, and reads and writes
// the catalog through its agent. Its copy must hold the Library's last
// confirmed write before it reads, as a library Job's must, and the close of
// the run waits for a catalog pod to confirm what it wrote.
func buildTitlesPlaceJob(library *Library, images jobImages, bus busEndpoint, languages []string, sync syncTarget,
	model time.Time, counts titlesReadings, created time.Time) *Job {
	// The annotation holds the fields of a titlesPlaced that the trigger
	// compares, and no counts of a run that has not happened.
	started, _ := json.Marshal(struct {
		Model         time.Time `json:"model"`
		Readings      int       `json:"readings"`
		NewestReading time.Time `json:"newestReading,omitzero"`
	}{model, counts.Readings, counts.NewestReading})
	agent := libraryJobAgent(images.corrosion)
	env := []EnvVar{
		{Name: catalogAPIVariable, Value: defaultCatalogAPI},
		{Name: syncActorVariable, Value: sync.actor},
		{Name: syncVersionVariable, Value: strconv.FormatInt(sync.version, 10)},
		{Name: syncTimeoutVariable, Value: defaultSyncTimeout.String()},
		{Name: handoffTimeoutVariable, Value: defaultHandoffTimeout.String()},
		{Name: titlesModelFittedVariable, Value: model.UTC().Format(time.RFC3339Nano)},
	}
	if len(languages) > 0 {
		env = append(env, EnvVar{Name: librarySubtitleLanguagesVariable, Value: strings.Join(languages, ",")})
	}
	return buildTitlesJob(library, images, titlesJobSpec{
		mode:        titlesPlaceMode,
		timeout:     titlesPlaceTimeout,
		annotations: map[string]string{titlesPlacingAnnotation: string(started)},
		scratch:     titlesPlaceScratch,
		env:         append(env, bus.env()...),
		readOnly:    true,
		agent:       &agent,
		resources: ResourceRequirements{
			Requests: map[string]string{"cpu": titlesPlaceCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": titlesPlaceMemoryLimit},
		},
	}, created)
}
