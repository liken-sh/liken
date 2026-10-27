package main

import "testing"

// How an edit of a Library reaches the worker of a fact that runs.

// A trickplay worker of the movies Library on a list of 5 videos, at the
// parallelism and in the state given, held by the cluster.
func heldTrickplayWorker(cluster *fakeCluster, parallelism int, status JobStatus) Job {
	job := trickplayWorkerJob(5, parallelism)
	job.Status = status
	cluster.holdJob(job)
	return *job
}

// A worker that runs takes the parallelism the Library asks for now, up to
// the videos of its list. A finished worker starts no pod, so it keeps the
// parallelism it ran at.
func TestARunningWorkerTakesTheLibrarysParallelism(t *testing.T) {
	cases := []struct {
		name    string
		asked   int
		running int
		status  JobStatus
		want    int32
	}{
		{name: "raised", asked: 3, running: 1, status: JobStatus{Active: 1}, want: 3},
		{name: "lowered", asked: 1, running: 3, status: JobStatus{Active: 3}, want: 1},
		{name: "above the list", asked: 8, running: 1, status: JobStatus{Active: 1}, want: 5},
		{name: "finished", asked: 3, running: 1, status: succeededStatus(testNow), want: 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			library.Spec.Trickplay.Enabled = true
			library.Spec.Trickplay.Parallelism = one.asked
			operator := testOperator(t, cluster)
			job := heldTrickplayWorker(cluster, one.running, one.status)

			if err := operator.runFactWorkers(t.Context(), library, listedReport(), []Job{job}, nil,
				testNow); err != nil {
				t.Fatal(err)
			}

			held := cluster.heldJob("house", job.Metadata.Name)
			if *held.Spec.Parallelism != one.want {
				t.Errorf("parallelism = %d, want %d", *held.Spec.Parallelism, one.want)
			}
		})
	}
}

// The change of a running worker's parallelism prints one line with the
// before and the after.
func TestAWorkersNewParallelismIsLogged(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Trickplay.Enabled = true
	library.Spec.Trickplay.Parallelism = 3
	operator, logged := loggingOperator(t, cluster)
	job := heldTrickplayWorker(cluster, 1, JobStatus{Active: 1})

	if err := operator.runFactWorkers(t.Context(), library, listedReport(), []Job{job}, nil, testNow); err != nil {
		t.Fatal(err)
	}

	wantOneLine(t, logged, "library house/movies: changed the trickplay worker "+job.Metadata.Name,
		"from 1 to 3 videos at once")
}

// Turning a fact off deletes its worker that runs, so a worker that runs
// away stops at the edit and does not run to the end of its list. A finished
// worker stays for a person to read.
func TestTurningAFactOffStopsItsWorker(t *testing.T) {
	cases := []struct {
		name   string
		status JobStatus
		held   bool
	}{
		{name: "running", status: JobStatus{Active: 1}},
		{name: "finished", status: succeededStatus(testNow), held: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			operator := testOperator(t, cluster)
			job := heldTrickplayWorker(cluster, 1, one.status)

			if err := operator.runFactWorkers(t.Context(), library, listedReport(), []Job{job}, nil,
				testNow); err != nil {
				t.Fatal(err)
			}

			if held := cluster.heldJob("house", job.Metadata.Name) != nil; held != one.held {
				t.Errorf("held = %v, want %v", held, one.held)
			}
		})
	}
}

// The stop of a worker prints one line that names the fact turned off.
func TestAStoppedWorkerIsLogged(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator, logged := loggingOperator(t, cluster)
	job := heldTrickplayWorker(cluster, 1, JobStatus{Active: 1})

	if err := operator.runFactWorkers(t.Context(), library, listedReport(), []Job{job}, nil, testNow); err != nil {
		t.Fatal(err)
	}

	wantOneLine(t, logged, "library house/movies: deleted the trickplay worker "+job.Metadata.Name,
		"because the Library turned trickplay off")
}
