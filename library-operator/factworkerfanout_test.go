package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// The worker Job of a fact the Library spreads over several pods, and the Job
// of a fact it leaves at one.

// A movies Library with appearances on, on a GPU template, in the pods given.
// Zero leaves the field unset, as a Library from before the field has it.
func spreadAppearances(parallelism int) *Library {
	library := studioMovies()
	library.Spec.Appearances.Enabled = true
	library.Spec.Appearances.GPUResourceClaimTemplate = "appearances-gpu"
	library.Spec.Appearances.Parallelism = parallelism
	return library
}

// The appearances worker of the Library, as JSON the API server reads.
func appearancesJobJSON(t *testing.T, library *Library) string {
	t.Helper()
	job := buildFactWorkerJob(library, appearancesWorker, jobImages{appearances: "appearances:test"},
		"http://library-operator.liken-system.svc/webhook/house/movies", "movies-walk-1", testNow)
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// A Library that sets no parallelism and a Library that sets 1 get the one
// Job the operator built before the field: one pod, the Job's own backoff,
// no index, and no spread.
func TestAWorkerOfOnePodIsTheJobOfBefore(t *testing.T) {
	unset, one := appearancesJobJSON(t, spreadAppearances(0)), appearancesJobJSON(t, spreadAppearances(1))

	if unset != one {
		t.Errorf("a parallelism of 1 builds\n%s\nwant the Job with none\n%s", one, unset)
	}
	for _, field := range []string{"completionMode", "completions", "parallelism", "backoffLimitPerIndex",
		"topologySpreadConstraints", workerParallelismVariable} {
		if strings.Contains(unset, field) {
			t.Errorf("the Job of one pod carries %s: %s", field, unset)
		}
	}
	if !strings.Contains(unset, `"backoffLimit":2`) {
		t.Errorf("the Job of one pod = %s, want the backoff limit of a worker", unset)
	}
}

// A Library that spreads a worker over three pods gets an Indexed Job of
// three completions, three at once, each index retried on its own.
func TestASpreadWorkerIsAnIndexedJob(t *testing.T) {
	job := buildFactWorkerJob(spreadAppearances(3), appearancesWorker, jobImages{}, "", "movies-walk-1", testNow)
	spec := job.Spec

	if spec.CompletionMode != indexedCompletion || *spec.Completions != 3 || *spec.Parallelism != 3 {
		t.Errorf("job = %s with %d completions and %d at once, want Indexed, 3 and 3",
			spec.CompletionMode, *spec.Completions, *spec.Parallelism)
	}
	if spec.BackoffLimit != nil || *spec.BackoffLimitPerIndex != scanBackoffLimit {
		t.Errorf("backoff = %v, per index %d, want none for the Job and the worker's for each index",
			spec.BackoffLimit, *spec.BackoffLimitPerIndex)
	}
	if *spec.ActiveDeadlineSeconds != int64(factWorkerDeadline/time.Second) {
		t.Errorf("deadline = %d, want the worker's for the whole Job", *spec.ActiveDeadlineSeconds)
	}
	if env := envOf(spec.Template.Spec.Containers[0]); env[workerParallelismVariable] != "3" {
		t.Errorf("env = %v, want the count of pods", env)
	}
}

// The pods of a spread worker prefer different nodes, and still run where a
// node already holds one, so a cluster with fewer GPUs than pods runs them
// all on the GPUs it has.
func TestTheSpreadPodsPreferDifferentNodes(t *testing.T) {
	library := spreadAppearances(3)
	pod := buildFactWorkerJob(library, appearancesWorker, jobImages{}, "", "movies-walk-1", testNow).Spec.Template

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
// list back while any pod of the worker runs or waits out its backoff.
func TestASpreadWorkerHoldsTheNextListUntilEveryIndexEnds(t *testing.T) {
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

			_, due := factWorkerDue(trickplayMovies(), trickplayWorker, listedReport(3), []Job{worker}, "")

			if due != one.due {
				t.Errorf("due = %v, want %v", due, one.due)
			}
		})
	}
}

// The pass names the count of pods in its line when it spreads a worker.
func TestThePassNamesThePodsOfASpreadWorker(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Trickplay.Enabled = true
	library.Spec.Trickplay.Parallelism = 3
	operator, logged := loggingOperator(t, cluster)

	if err := operator.runFactWorkers(t.Context(), library, listedReport(3), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	created := cluster.heldJobs()
	if len(created) != 1 || *created[0].Spec.Parallelism != 3 {
		t.Fatalf("jobs = %+v, want one trickplay worker of 3 pods", created)
	}
	wantOneLine(t, logged, "library house/movies: created the job "+created[0].Metadata.Name,
		"to work the trickplay gap of 3 videos in 3 pods from the list of the job movies-walk-1")
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
				t.Errorf("the operator reads %d pods, want 16", got)
			}
		})
	}
}

// A pod whose index the Job did not give it right is a Job to repair, so the
// container fails before it reads the volume.
func TestAWorkerWithABadIndexFails(t *testing.T) {
	t.Setenv(libraryFactVariable, factAppearances)
	t.Setenv(completionIndexVariable, "3")
	t.Setenv(workerParallelismVariable, "3")

	_, err := newFactWorkerRun(io.Discard)

	if err == nil || !strings.Contains(err.Error(), "no index of 3 pods") {
		t.Errorf("newFactWorkerRun = %v, want the index refused", err)
	}
}

// A pod of a Job of several reads its share and marks its temporaries with
// its index.
func TestAWorkerReadsItsShareOutOfTheEnvironment(t *testing.T) {
	t.Setenv(libraryFactVariable, factAppearances)
	t.Setenv(jobNameVariable, "movies-appearances-1")
	t.Setenv(completionIndexVariable, "1")
	t.Setenv(workerParallelismVariable, "3")

	work, err := newFactWorkerRun(io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if work.share != (workerShare{index: 1, count: 3}) || work.writer.job != "movies-appearances-1-appearances-1" {
		t.Errorf("share = %+v with writer %q, want index 1 of 3 and its own writer", work.share, work.writer.job)
	}
}
