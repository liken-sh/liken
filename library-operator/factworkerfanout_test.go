package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// How many videos of a list a worker runs at once, and where its pods go.

// A movies Library with appearances on, on a GPU template, at the parallelism
// given.
func spreadAppearances(parallelism int) *Library {
	library := studioMovies()
	library.Spec.Appearances.Enabled = true
	library.Spec.Appearances.GPUResourceClaimTemplate = "appearances-gpu"
	library.Spec.Appearances.Parallelism = parallelism
	return library
}

// The pods of a worker prefer different nodes, and still run where a node
// already holds one, so a cluster with fewer GPUs than running pods runs
// them all on the GPUs it has.
func TestTheWorkerPodsPreferDifferentNodes(t *testing.T) {
	pod := buildFactWorkerJob(spreadAppearances(3), appearancesWorker, jobImages{}, testJobBus, "", enrichedRun(),
		10, testNow).Spec.Template

	spread := pod.Spec.TopologySpreadConstraints
	if len(spread) != 1 {
		t.Fatalf("spread = %+v, want one constraint", spread)
	}
	one := spread[0]
	if one.MaxSkew != 1 || one.TopologyKey != hostnameTopologyKey || one.WhenUnsatisfiable != "ScheduleAnyway" {
		t.Errorf("constraint = %+v, want a skew of 1 over the node's hostname, scheduled anyway", one)
	}
	for key, value := range one.LabelSelector.MatchLabels {
		if pod.Metadata.Labels[key] != value {
			t.Errorf("the selector names %s=%s, which the worker's own pods do not carry", key, value)
		}
	}
	if pod.Spec.ResourceClaims[0].ResourceClaimTemplateName != "appearances-gpu" {
		t.Errorf("claims = %+v, want each pod's claim from the worker's template", pod.Spec.ResourceClaims)
	}
}

// A Library that sets no parallelism runs one video at a time.
func TestAnUnsetParallelismIsOneVideoAtATime(t *testing.T) {
	job := buildFactWorkerJob(spreadAppearances(0), appearancesWorker, jobImages{}, testJobBus, "", enrichedRun(),
		10, testNow)

	if got := *job.Spec.Parallelism; got != 1 {
		t.Errorf("parallelism = %d, want 1", got)
	}
}

// Each worker fact reads its own count from its own block.
func TestEachWorkerReadsItsOwnParallelism(t *testing.T) {
	library := studioMovies()
	library.Spec.Trickplay.Parallelism = 2
	library.Spec.Appearances.Parallelism = 4

	if got := trickplayWorker.parallelism(library); got != 2 {
		t.Errorf("trickplay = %d, want 2", got)
	}
	if got := appearancesWorker.parallelism(library); got != 4 {
		t.Errorf("appearances = %d, want 4", got)
	}
}

// An Indexed Job takes its Complete or Failed condition only when every
// index has ended, so the due rule that reads the conditions holds the next
// worker back while any pod of the worker runs or waits out its backoff.
func TestAWorkerHoldsTheNextWorkerUntilEveryIndexEnds(t *testing.T) {
	cases := []struct {
		name   string
		status JobStatus
		due    bool
	}{
		{name: "two indexes running and one done", status: JobStatus{Active: 2, Succeeded: 1}},
		{name: "one index waiting out its backoff", status: JobStatus{Succeeded: 2, Failed: 1}},
		{name: "every index done", status: succeededStatus(testNow), due: true},
		{name: "one index out of retries", status: JobStatus{Succeeded: 2, Failed: 3,
			Conditions: []JobCondition{{Type: jobFailed, Status: ConditionTrue, Reason: "FailedIndexes"}}}, due: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			worker := trickplayJob(one.status, "movies-walk-0")

			due := factWorkerDue(trickplayMovies(), trickplayWorker, enrichedRun(), 3, []Job{worker}, "")

			if due != one.due {
				t.Errorf("due = %v, want %v", due, one.due)
			}
		})
	}
}

// The count the API server admits is the count the operator reads: an
// integer from 1 to 16 under each worker fact's block, 1 when unset.
func TestTheOperatorReadsAParallelismTheSchemaAdmits(t *testing.T) {
	for _, worker := range factWorkers {
		t.Run(worker.fact, func(t *testing.T) {
			field := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
				"spec", "properties", worker.fact, "properties", "parallelism").(map[string]any)
			if field["type"] != "integer" || field["default"] != 1 || field["minimum"] != 1 ||
				field["maximum"] != 16 {
				t.Errorf("parallelism = %+v, want an integer from 1 to 16 with a default of 1", field)
			}

			library := studioMovies()
			body := `{"` + worker.fact + `":{"enabled":true,"parallelism":16}}`
			if err := json.Unmarshal([]byte(body), &library.Spec); err != nil {
				t.Fatal(err)
			}
			if got := worker.parallelism(library); got != 16 {
				t.Errorf("the operator reads %d videos at once, want 16", got)
			}
		})
	}
}

// A pod with no index, or no list, is a Job to repair, so the container
// fails before it reads the bus.
func TestAWorkerWithNoIndexOrListFails(t *testing.T) {
	cases := []struct {
		name  string
		index string
		list  string
		want  string
	}{
		{name: "no index", list: "movies-walk-1", want: completionIndexVariable},
		{name: "an index that is not a number", index: "first", list: "movies-walk-1", want: completionIndexVariable},
		{name: "no list", index: "0", want: workListVariable},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Setenv(libraryFactVariable, factAppearances)
			t.Setenv(completionIndexVariable, one.index)
			t.Setenv(workListVariable, one.list)

			_, err := newFactWorkerRun(io.Discard)

			if err == nil || !strings.Contains(err.Error(), one.want) {
				t.Errorf("newFactWorkerRun = %v, want %s refused", err, one.want)
			}
		})
	}
}

// A pod reads its list and index, and marks its temporaries with its index.
func TestAWorkerReadsItsIndexOutOfTheEnvironment(t *testing.T) {
	t.Setenv(libraryFactVariable, factAppearances)
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "series")
	t.Setenv(jobNameVariable, "series-appearances-1")
	t.Setenv(completionIndexVariable, "41")
	t.Setenv(workListVariable, "series-walk-1")

	work, err := newFactWorkerRun(io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if work.list != testWorkList || work.index != 41 || work.base != defaultTopicBase {
		t.Errorf("worker reads %+v at index %d under %q, want %+v at 41", work.list, work.index, work.base,
			testWorkList)
	}
	if work.writer.job != "series-appearances-1-appearances-41" {
		t.Errorf("writer = %q, want the Job's name, the fact, and the index", work.writer.job)
	}
}
