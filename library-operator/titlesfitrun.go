package main

// titlesfitrun.go is the container of a Library's fit Job (titlesfitjob.go).
// It fits the model that places the Library's end titles, on the Library's
// own volume and on nothing else. The volume holds every fact, so the run
// reads no catalog: it walks the root for the readings the titles worker
// wrote, takes each video's label from the marks ledger of the video's
// folder, and runs `titles train` on them. A model that passes the guard
// takes the name .liken/titles/model.json under the root, and the run
// publishes what it fitted on the bus, where the operator reads it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The role a fit Job's container runs.
const titlesFitMode = "titles-fit"

// How long the fit may run. Plan 96 measured minutes for a library of 1,297
// marked videos, and the fit's cost grows with the videos, so the timeout
// bounds only a fit that hangs.
const titlesFitTimeout = 6 * time.Hour

// The threads `titles train` fits on. The fit of each tree is spread over
// them, and the container states no CPU limit, so the count sets how much of
// a node one fit takes.
const titlesFitThreads = 4

// The share of the held-out videos that a model must place within 30 seconds
// of their mark, or the run leaves the model the Library has. The held-out
// videos are the ones the fit did not train on, and the score compares the
// model's placement with the providers' marks.
//
// The floor catches a broken fit: a bug in the readings, in the marks, or in
// the trainer. A model from a broken fit places almost no video near its
// mark. The floor is not there to catch loose marks. The providers' marks
// are often looser than the real start of the credits, so a model that
// works can score low against them and still place the credits well. So the
// floor of 30% is far under what a working model scores against loose marks,
// and far over what a broken model scores. Plan 96 holds the measurements.
//
// It is a floor and not a comparison with the model before, because the
// score cannot rank two models that work.
const titlesFitFloor = 0.30

// The emptyDir the fit writes its labels file in. The image is a closure on
// scratch and holds no temporary directory, and the labels are no fact of
// the Library, so they do not go on the volume.
const titlesFitScratch = "/var/cache/titles-fit"

// The model's file, in .liken/titles/ under the Library's root.
const titlesModelName = "model.json"

// The providers whose mark is a label, the better first. TheIntroDB matches
// a mark to the file by its length, and IntroDB by the work alone, so where
// both mark a video the first is the label.
var titlesLabelSources = []string{"theintrodb", "introdb"}

// What one fit left, and the payload of its message on the bus.
type titlesFit struct {
	// The titles the model places.
	Kind string `json:"kind"`
	// fitted for a model that took the Library's name, rejected for one
	// under the floor, and failed for a fit that wrote none.
	Result string `json:"result"`
	// Why the fit was rejected or failed, in a sentence.
	Reason string    `json:"reason,omitempty"`
	Job    string    `json:"job,omitempty"`
	At     time.Time `json:"at"`
	// The videos with a reading and a mark that the run found, and their
	// films and series. A fit counts its growth from these.
	Marked titlesMarked `json:"marked"`
	// The videos the model was fitted on, and the marks the fit left out
	// because a first model placed their titles far from them and was sure.
	Videos  int `json:"videos"`
	LeftOut int `json:"leftOut"`
	// How close the fit placed the videos it was not fitted on to their
	// marks: how many of them, the share within 3, 10, and 30 seconds, and
	// the median distance in seconds.
	HeldOut  int     `json:"heldOut"`
	Within3  float64 `json:"within3"`
	Within10 float64 `json:"within10"`
	Within30 float64 `json:"within30"`
	Median   float64 `json:"median"`
}

const (
	titlesFitFitted   = "fitted"
	titlesFitRejected = "rejected"
	titlesFitFailed   = "failed"
)

// The kind of titles the tool places, in the word its model file states.
const titlesKindEnd = "end-titles"

// The part of a model file the run reads: what `train` says it fitted on and
// how well it placed the videos it held out.
type titlesModelFile struct {
	Kind    string `json:"kind"`
	Trained struct {
		Videos  int `json:"videos"`
		LeftOut int `json:"left_out"`
	} `json:"trained"`
	HeldOut struct {
		Videos   int     `json:"videos"`
		Within3  float64 `json:"within_3"`
		Within10 float64 `json:"within_10"`
		Within30 float64 `json:"within_30"`
		Median   float64 `json:"median"`
	} `json:"held_out"`
}

// One fit container: the Library it fits for, its volume, and how it reaches
// the broker.
type titlesFitRun struct {
	namespace string
	name      string
	kind      string
	root      string
	job       string
	writer    *volumeWriter
	log       io.Writer
	base      string
	dial      func(context.Context) (net.Conn, error)
	// A directory of the container's own, for the labels file.
	scratch string
}

// The role's whole program. A failure is a non-zero exit, so the Job retries
// it, up to its backoff.
func runTitlesFit() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	bus := busEndpointOf(os.Getenv(busAddressVariable), os.Getenv(topicBaseVariable))
	run := &titlesFitRun{
		namespace: os.Getenv(libraryNamespaceVariable),
		name:      os.Getenv(libraryNameVariable),
		kind:      os.Getenv(libraryKindVariable),
		root:      path.Join(libraryMountPath, os.Getenv(libraryRootVariable)),
		job:       os.Getenv(jobNameVariable),
		writer:    newVolumeWriter(writerName(os.Getenv(jobNameVariable), titlesFitMode)),
		log:       os.Stdout,
		base:      bus.base,
		dial:      bus.dial,
		scratch:   titlesFitScratch,
	}
	if err := run.fit(stopped, time.Now().UTC()); err != nil {
		run.logf("the titles fit failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// One label: a video, the second a provider says its end titles start, and
// the film or series the video is of. `train` holds out a film or a series
// whole, so that no model is scored on a title it was fitted on.
type titlesLabel struct {
	video string
	start float64
	group string
}

// The whole run. A fit that fails, or a model under the floor, is a result
// like a fitted one: the run publishes it and ends well, because a retry
// would read the same videos and fit the same model. Only a broker the run
// cannot reach fails the run, so the Job retries the publish.
func (r *titlesFitRun) fit(ctx context.Context, now time.Time) error {
	labels, err := r.labels()
	if err != nil {
		return err
	}
	fit := titlesFit{Kind: titlesKindEnd, Job: r.job, At: now, Marked: countLabels(labels)}
	r.logf("fitting the titles model on %s of %d films and series", counted(fit.Marked.Videos, "video"),
		fit.Marked.Groups)
	// `train` writes the model under a temporary name of this writer, which
	// takes the model's name only after the guard has read it, so `place`
	// never opens a model the guard did not pass.
	target := filepath.Join(r.root, likenDirectory, titlesRecordsDirectory, titlesModelName)
	candidate := r.writer.temporary(target)
	r.removeEarlierCandidates(filepath.Dir(target))
	timed, cancel := context.WithTimeout(ctx, titlesFitTimeout)
	defer cancel()
	model, err := r.train(timed, labels, candidate)
	switch {
	case err != nil && ctx.Err() != nil:
		// A Job that is deleted stops the fit in the middle, and the tool's
		// error is then the kill. The run says nothing, and the next Job
		// fits.
		r.writer.removeTemporary(candidate)
		return ctx.Err()
	case err != nil:
		r.writer.removeTemporary(candidate)
		fit.Result, fit.Reason = titlesFitFailed, titlesReason(err)
		r.logf("could not fit the titles model: %v", err)
	case model.HeldOut.Within30 < titlesFitFloor:
		r.writer.removeTemporary(candidate)
		fit.take(model)
		fit.Result = titlesFitRejected
		fit.Reason = fmt.Sprintf("the model placed %.0f%% of the %d held-out videos within 30 seconds of their marks, "+
			"under the floor of %.0f%%", 100*model.HeldOut.Within30, model.HeldOut.Videos, 100*titlesFitFloor)
		r.logf("left the model as it was: %s", fit.Reason)
	default:
		if err := r.writer.land(candidate, target); err != nil {
			return fmt.Errorf("naming the model: %w", err)
		}
		fit.take(model)
		fit.Result = titlesFitFitted
		r.logf("fitted the titles model on %s, and it placed %.0f%% of the %d held-out videos within 10 seconds "+
			"of their marks and %.0f%% within 30", counted(fit.Videos, "video"), 100*fit.Within10, fit.HeldOut,
			100*fit.Within30)
	}
	return r.publish(ctx, fit)
}

// removeEarlierCandidates removes the models that earlier fits left under a
// temporary name when they were killed. One fit of a Library runs at a time
// (titlesfitjob.go), and nothing else writes a temporary into the model's
// directory, so a temporary there belongs to no run.
func (r *titlesFitRun) removeEarlierCandidates(directory string) {
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), likenTempMark) {
			r.writer.removeTemporary(filepath.Join(directory, entry.Name()))
		}
	}
}

// What `train` says of the model, into the result.
func (f *titlesFit) take(model titlesModelFile) {
	f.Videos, f.LeftOut = model.Trained.Videos, model.Trained.LeftOut
	f.HeldOut = model.HeldOut.Videos
	f.Within3, f.Within10, f.Within30 = model.HeldOut.Within3, model.HeldOut.Within10, model.HeldOut.Within30
	f.Median = model.HeldOut.Median
}

// How many videos the labels name, and how many films and series.
func countLabels(labels []titlesLabel) titlesMarked {
	groups := map[string]bool{}
	for _, label := range labels {
		groups[label.group] = true
	}
	return titlesMarked{Videos: len(labels), Groups: len(groups)}
}

// Every video under the root with a reading and a label, in the order of
// its path. The walk reads only directory listings and marks ledgers: a
// reading is named for its video, so no reading is opened here.
func (r *titlesFitRun) labels() ([]titlesLabel, error) {
	var labels []titlesLabel
	err := filepath.WalkDir(r.root, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A folder that left the volume during the walk holds no label.
			// A root that is not there is a volume the pod did not mount.
			if errors.Is(err, fs.ErrNotExist) && at != r.root {
				return nil
			}
			return err
		}
		if !entry.IsDir() || entry.Name() != likenDirectory {
			return nil
		}
		readings, _ := os.ReadDir(filepath.Join(at, titlesRecordsDirectory))
		for _, reading := range readings {
			name, found := strings.CutSuffix(reading.Name(), ".jsonl")
			if !found || !reading.Type().IsRegular() || isPartialName(reading.Name()) {
				continue
			}
			video := filepath.Join(filepath.Dir(at), name)
			if label, held := r.labelOf(video); held {
				labels = append(labels, label)
			}
		}
		return filepath.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("reading the readings under the root: %w", err)
	}
	slices.SortFunc(labels, func(a, b titlesLabel) int { return strings.Compare(a.video, b.video) })
	return labels, nil
}

// The label of one video: the start of the mark of its end titles from the
// best provider that has one, and its film or series. A video with no such
// mark has none, and the model places it once it is fitted.
func (r *titlesFitRun) labelOf(video string) (titlesLabel, bool) {
	folder, entry := likenFolderFor(r.kind, video)
	ledger, err := readLikenLedger(folder, factMarks)
	if err != nil {
		return titlesLabel{}, false
	}
	group, held := titleFolderOf(r.root, r.kind, video)
	if !held {
		group = filepath.Dir(video)
	}
	for _, source := range titlesLabelSources {
		for _, mark := range ledger.Marks {
			if mark.Path == entry && mark.Kind == markKindCredits && mark.Source == source && mark.Start != nil {
				return titlesLabel{video: video, start: float64(*mark.Start) / 1000, group: group}, true
			}
		}
	}
	return titlesLabel{}, false
}

// One run of `titles train` on the labels, and what the model it wrote says
// of itself. The labels file is the tool's own form: a video, a start, and a
// group on each line, with tabs between them.
func (r *titlesFitRun) train(ctx context.Context, labels []titlesLabel, out string) (titlesModelFile, error) {
	var lines strings.Builder
	for _, label := range labels {
		fmt.Fprintf(&lines, "%s\t%s\t%s\n", label.video, strconv.FormatFloat(label.start, 'f', 3, 64), label.group)
	}
	file := filepath.Join(r.scratch, "titles-labels.tsv")
	if err := os.WriteFile(file, []byte(lines.String()), 0o600); err != nil {
		return titlesModelFile{}, fmt.Errorf("writing the labels: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return titlesModelFile{}, fmt.Errorf("making the model's directory: %w", err)
	}
	err := runTitlesTool(ctx, "train", "--labels", file, "--out", out, "--threads", strconv.Itoa(titlesFitThreads))
	if err != nil {
		return titlesModelFile{}, err
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return titlesModelFile{}, fmt.Errorf("reading the model the fit wrote: %w", err)
	}
	var model titlesModelFile
	if err := json.Unmarshal(data, &model); err != nil {
		return titlesModelFile{}, fmt.Errorf("reading the model the fit wrote: %w", err)
	}
	return model, nil
}

// The result, retained, so an operator that restarts reads the last fit of
// each Library back from the broker.
func (r *titlesFitRun) publish(ctx context.Context, fit titlesFit) error {
	return publishTitlesResult(ctx, r.dial, r.job+"-fit", titlesFitTopic(r.base, r.namespace, r.name), fit)
}

// One line of this run, with every path in an error made opaque, the rule
// every container's log follows.
func (r *titlesFitRun) logf(format string, args ...any) {
	if r.log == nil {
		return
	}
	fmt.Fprintf(r.log, "library.liken.sh: "+format+"\n", opaqueErrors(args, r.root)...)
}
