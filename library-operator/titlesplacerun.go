package main

// titlesplacerun.go is the container of a Library's placing Job
// (titlesplacejob.go). It places the end titles of every reading of the
// Library with the Library's model, in one run of `titles place`, because
// `place` places the episodes of a folder together. It writes each
// placement into the marks table and derives the Library's estimates again,
// because the estimate reads the placements. Then it hands off, as every Job
// that writes the catalog does, and publishes its result on the bus, where
// the operator reads it.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
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

// One placing container: the Library it places for, its volume and its
// copy of the catalog, the model it places with, and how it reaches the
// broker.
type titlesPlaceRun struct {
	namespace string
	name      string
	root      string
	job       string
	catalog   *Catalog
	// The Library's last confirmed write, which the copy must hold before
	// the run reads it, and the bounds of that wait and of the hand-off.
	sync           syncTarget
	syncTimeout    time.Duration
	handoffTimeout time.Duration
	// When the model was fitted, as the operator gave it.
	model time.Time
	// The household's subtitle languages, which the mark estimates read.
	languages []string
	log       io.Writer
	base      string
	dial      func(context.Context) (net.Conn, error)
	// A directory of the container's own, for the list of videos.
	scratch string
}

// The role's whole program. A run that cannot read or write the catalog, or
// reach the broker, fails, so the Job retries it, up to its backoff.
func runTitlesPlace() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	bus := busEndpointOf(os.Getenv(busAddressVariable), os.Getenv(topicBaseVariable))
	api := os.Getenv(catalogAPIVariable)
	if api == "" {
		api = defaultCatalogAPI
	}
	model, _ := time.Parse(time.RFC3339Nano, os.Getenv(titlesModelFittedVariable))
	run := &titlesPlaceRun{
		namespace:      os.Getenv(libraryNamespaceVariable),
		name:           os.Getenv(libraryNameVariable),
		root:           path.Join(libraryMountPath, os.Getenv(libraryRootVariable)),
		job:            os.Getenv(jobNameVariable),
		catalog:        NewCatalog(api, &http.Client{Timeout: catalogWriteTimeout}),
		sync:           syncTargetOf(os.Getenv(syncActorVariable), os.Getenv(syncVersionVariable)),
		syncTimeout:    syncTimeout(os.Getenv(syncTimeoutVariable)),
		handoffTimeout: handoffTimeout(os.Getenv(handoffTimeoutVariable)),
		model:          model,
		languages:      commaNames(os.Getenv(librarySubtitleLanguagesVariable)),
		log:            os.Stdout,
		base:           bus.base,
		dial:           bus.dial,
		scratch:        titlesPlaceScratch,
	}
	if err := run.place(stopped); err != nil {
		run.logf("the placing of the end titles failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// The whole run. A model that is not on the volume, and a `place` that
// fails, are a result like a placing that worked: the run publishes it and
// ends well, because a retry would read the same readings with the same
// model. The result records the model and the readings, so the operator
// starts no run on them again.
func (r *titlesPlaceRun) place(ctx context.Context) error {
	if err := awaitCatalogSync(ctx, r.catalog, r.sync, r.syncTimeout); err != nil {
		return err
	}
	library := libraryKey(r.namespace, r.name)
	run := libraryRun{Worker: titlesPlaceMode, Job: r.job, Started: time.Now().UTC()}
	if _, _, err := r.catalog.UpsertRun(ctx, library, run); err != nil {
		return fmt.Errorf("writing the run of %s: %w", library, err)
	}
	readings, err := r.catalog.titlesReadingList(ctx, library)
	if err != nil {
		return fmt.Errorf("reading the readings of %s: %w", library, err)
	}
	held, err := r.catalog.titlesPlacements(ctx, library)
	if err != nil {
		return fmt.Errorf("reading the placements of %s: %w", library, err)
	}
	placed := titlesPlaced{Result: titlesPlacePlaced, Model: r.model, Readings: len(readings),
		Placements: len(held)}
	for _, reading := range readings {
		if reading.at.After(placed.NewestReading) {
			placed.NewestReading = reading.at
		}
	}
	answers, err := r.answers(ctx, readings, &placed)
	switch {
	case err != nil && ctx.Err() != nil:
		// A Job that is deleted stops the tool in the middle, and the tool's
		// error is then the kill. The run says nothing, and the next Job
		// places.
		return ctx.Err()
	case err != nil:
		// The reason reaches the Library's status, so a path in it turns
		// opaque here, where the root is known.
		placed.Result, placed.Reason = titlesPlaceFailed, opaqueText(titlesReason(err), r.root)
		r.logf("could not place the end titles: %v", err)
	default:
		if err := r.write(ctx, library, answers, held, &placed); err != nil {
			return err
		}
		if err := deriveMarkEstimates(ctx, r.catalog, library, r.languages); err != nil {
			return fmt.Errorf("deriving the mark estimates of %s: %w", library, err)
		}
	}
	run.Finished = time.Now().UTC()
	run.Failure = placed.Reason
	if err := handOff(ctx, r.catalog, library, run, r.log, r.handoffTimeout); err != nil {
		return err
	}
	placed.At = run.Finished
	result := titlesPlacing{Kind: titlesKindEnd, Job: r.job, titlesPlaced: placed}
	return publishTitlesResult(ctx, r.dial, r.job+"-place", titlesPlaceTopic(r.base, r.namespace, r.name), result)
}

// One reading as the catalog lists it: the video's path under the root, and
// when its attempt landed.
type titlesReading struct {
	path string
	at   time.Time
}

// Every reading of one Library, in the order of the path.
func (c *Catalog) titlesReadingList(ctx context.Context, library string) ([]titlesReading, error) {
	var readings []titlesReading
	err := c.stream(ctx, `SELECT f.path, a.at `+titlesReadingsFrom+` ORDER BY f.path`, []any{library},
		func(cells []any) error {
			if len(cells) < 2 {
				return nil
			}
			path, _ := cells[0].(string)
			readings = append(readings, titlesReading{path: path, at: time.Unix(int64(cellNumber(cells[1])), 0).UTC()})
			return nil
		})
	return readings, err
}

// The placement rows one Library holds now, by the video's path.
func (c *Catalog) titlesPlacements(ctx context.Context, library string) (map[string]markRow, error) {
	held := map[string]markRow{}
	err := c.stream(ctx, `SELECT path, start_ms, confidence FROM marks `+titlesPlacementsWhere, []any{library},
		func(cells []any) error {
			if len(cells) < 3 {
				return nil
			}
			path, _ := cells[0].(string)
			row := markRow{Library: library, Path: path, Ordinal: titlesPlacementOrdinal,
				Kind: markKindCreditsPlacement, Source: markSourceTitles, Start: cellMillis(cells[1])}
			if confidence, ok := cells[2].(float64); ok {
				row.Confidence = &confidence
			}
			held[path] = row
			return nil
		})
	return held, err
}

// What `place` prints for one video.
type titlesAnswer struct {
	Video      string   `json:"video"`
	Start      *float64 `json:"start"`
	Confidence float64  `json:"confidence"`
}

// The answer of `place` for each reading the volume holds, by the video's
// path under the root. A reading that the catalog names and the volume does
// not hold is counted and left out, because `place` fails the whole run on
// a reading it cannot open.
func (r *titlesPlaceRun) answers(ctx context.Context, readings []titlesReading,
	placed *titlesPlaced) (map[string]titlesAnswer, error) {
	model := filepath.Join(r.root, likenDirectory, titlesRecordsDirectory, titlesModelName)
	if _, err := os.Stat(model); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("the volume holds no model at .liken/titles/" + titlesModelName + " under the root")
		}
		return nil, fmt.Errorf("reading the model: %w", err)
	}
	videos := map[string]string{}
	var lines strings.Builder
	for _, reading := range readings {
		video := filepath.Join(r.root, reading.path)
		record := filepath.Join(filepath.Dir(video), likenDirectory, titlesRecordsDirectory,
			filepath.Base(video)+".jsonl")
		if _, err := os.Stat(record); err != nil {
			placed.Missing++
			continue
		}
		videos[video] = reading.path
		fmt.Fprintln(&lines, video)
	}
	answers := map[string]titlesAnswer{}
	if len(videos) == 0 {
		return answers, nil
	}
	r.logf("placing the end titles of %s with the model fitted at %s", counted(len(videos), "reading"),
		r.model.UTC().Format(time.RFC3339))
	list := filepath.Join(r.scratch, "titles-videos.txt")
	if err := os.WriteFile(list, []byte(lines.String()), 0o600); err != nil {
		return nil, fmt.Errorf("writing the list of videos: %w", err)
	}
	timed, cancel := context.WithTimeout(ctx, titlesPlaceTimeout)
	defer cancel()
	output, err := runTitlesToolOutput(timed, "place", "--videos", list, "--model", model,
		"--threads", strconv.Itoa(titlesPlaceThreads))
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var answer titlesAnswer
		if err := json.Unmarshal(scanner.Bytes(), &answer); err != nil {
			return nil, fmt.Errorf("reading the answer of titles place: %w", err)
		}
		if path, held := videos[answer.Video]; held {
			answers[path] = answer
		}
	}
	return answers, scanner.Err()
}

// Writes the placement of each answer with a start, and deletes the
// placement of every video that has no start now or no reading. A video
// whose reading holds no end titles gets no row, because a mark with no
// start means "from the start of the file" to every reader. A row that holds
// the same start and confidence is not written again.
func (r *titlesPlaceRun) write(ctx context.Context, library string, answers map[string]titlesAnswer,
	held map[string]markRow, placed *titlesPlaced) error {
	var changed []markRow
	for path, answer := range answers {
		if answer.Start == nil {
			placed.None++
			continue
		}
		start := int64(math.Round(*answer.Start * 1000))
		confidence := answer.Confidence
		row := markRow{Library: library, Path: path, Ordinal: titlesPlacementOrdinal, Kind: markKindCreditsPlacement,
			Start: &start, Source: markSourceTitles, Confidence: &confidence}
		if confidence >= titlesSureConfidence {
			placed.Sure++
		}
		if before, standing := held[path]; !standing || !samePlacement(before, row) {
			changed = append(changed, row)
		}
		delete(held, path)
	}
	slices.SortFunc(changed, func(a, b markRow) int { return strings.Compare(a.Path, b.Path) })
	gone := make([]markKey, 0, len(held))
	for path := range held {
		gone = append(gone, markKey{Path: path, Ordinal: titlesPlacementOrdinal})
	}
	slices.SortFunc(gone, func(a, b markKey) int { return strings.Compare(a.Path, b.Path) })
	if len(changed) > 0 {
		if _, err := r.catalog.UpsertMarks(ctx, nil, changed); err != nil {
			return fmt.Errorf("writing the placements of %s: %w", library, err)
		}
	}
	if len(gone) > 0 {
		if _, err := r.catalog.DeleteMarks(ctx, library, gone); err != nil {
			return fmt.Errorf("deleting the placements of %s: %w", library, err)
		}
	}
	placed.Placements = len(answers) - placed.None
	r.logf("placed the end titles of %s: %s, %d of them sure, and %d with no end titles; wrote %s and deleted %d",
		counted(len(answers), "reading"), counted(placed.Placements, "placement"), placed.Sure, placed.None,
		counted(len(changed), "row"), len(gone))
	return nil
}

func samePlacement(a, b markRow) bool {
	if !sameEdge(a.Start, b.Start) || (a.Confidence == nil) != (b.Confidence == nil) {
		return false
	}
	return a.Confidence == nil || *a.Confidence == *b.Confidence
}

// One line of this run, with every path in an error made opaque, the rule
// every container's log follows.
func (r *titlesPlaceRun) logf(format string, args ...any) {
	if r.log == nil {
		return
	}
	fmt.Fprintf(r.log, "library.liken.sh: "+format+"\n", opaqueErrors(args, r.root)...)
}
