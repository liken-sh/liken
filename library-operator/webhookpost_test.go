package main

// The tests of the post a walk Job makes to its Library's webhooks: the body
// a receiver gets, the bearer token, the retry and its bound, the runs row of
// the delivery, and the Jobs that post nothing.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// A receiver that answers each request with the next status of its script,
// and with the last one after the script runs out, and records what it got.
type fakeReceiver struct {
	mutex    sync.Mutex
	statuses []int
	bodies   [][]byte
	tokens   []string
}

func (f *fakeReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.bodies = append(f.bodies, body)
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))
	status := f.statuses[min(len(f.bodies), len(f.statuses))-1]
	w.WriteHeader(status)
}

func (f *fakeReceiver) requests() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return len(f.bodies)
}

// The last body the receiver got, decoded.
func (f *fakeReceiver) lastBody(t *testing.T) walkBody {
	t.Helper()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if len(f.bodies) == 0 {
		t.Fatal("the receiver got no request")
	}
	var body walkBody
	if err := json.Unmarshal(f.bodies[len(f.bodies)-1], &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The close container of a walk whose Library posts to one receiver with the
// token named, and the receiver.
func postingJob(t *testing.T, catalog *Catalog, token string, statuses ...int) (*closeRun, *fakeReceiver) {
	t.Helper()
	run, _ := closingJob(t, catalog)
	receiver := &fakeReceiver{statuses: statuses}
	run.webhooks = webhookPoster{
		targets: []webhookTarget{{url: "http://receiver.test/library", token: token}},
		client:  apiservertest.Start(t, receiver).Client(),
	}
	return run, receiver
}

// The note the scan writes, read from the catalog as it is now.
func noteVideosNow(t *testing.T, run *closeRun, catalog *Catalog) {
	t.Helper()
	states, err := catalog.videoStates(t.Context(), run.library)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(states)
	if err := run.board.writeNote(videosBeforeNote, data); err != nil {
		t.Fatal(err)
	}
}

// Runs the close container to its end, confirming each run it hands off.
func runClosing(t *testing.T, run *closeRun, catalog *Catalog, workers ...string) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()
	for _, worker := range append([]string{workerEnrich}, workers...) {
		confirmTheRun(t, catalog, worker, run.job)
	}
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}
}

func TestAWalkPostsEveryTitleItChangedWithTheToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		seedMovie(t, catalog, "movie:tmdb:1001", "The Long Survey (1999)", 700)
		seedMovie(t, catalog, "movie:tmdb:1003", "A Quiet Shore (1999)", 400)
		run, receiver := postingJob(t, catalog, "s3cret", http.StatusNoContent)
		noteVideosNow(t, run, catalog)
		seedMovie(t, catalog, "movie:tmdb:1002", "Another Survey (1999)", 300)
		seedVideo(t, catalog, "The Long Survey (1999)/movie.mkv", 900, "movie:tmdb:1001")

		runClosing(t, run, catalog, workerWebhooks)

		body := receiver.lastBody(t)
		var got []string
		for _, title := range body.Titles {
			got = append(got, title.Row["id"].(string)+" "+title.Change+" "+title.Files[0].Change)
		}
		want := []string{"movie:tmdb:1001 changed replaced", "movie:tmdb:1002 added added"}
		if !slices.Equal(got, want) {
			t.Errorf("titles = %v, want %v", got, want)
		}
		if body.Namespace != "house" || body.Library != "movies" || body.Walk != walkFull || body.Job != run.job {
			t.Errorf("body = %s/%s %s %s, want house/movies full %s",
				body.Namespace, body.Library, body.Walk, body.Job, run.job)
		}
		if receiver.tokens[0] != "Bearer s3cret" {
			t.Errorf("Authorization = %q, want the bearer token", receiver.tokens[0])
		}
	})
}

// The delivery is a runs row of its own, so status.runs shows the last one and
// the address that refused it. A refusal does not fail the Job.
func TestADeliveryIsARunsRowThatNamesTheAddressThatRefused(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int
		requests int
		failure  string
	}{
		{"an address that answers", []int{http.StatusOK}, 1, ""},
		{"an address that recovers", []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK}, 3, ""},
		{"an address that refuses the request", []int{http.StatusUnauthorized}, 1,
			"webhook 1 (receiver.test): the receiver answered 401 Unauthorized"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				run, receiver := postingJob(t, catalog, "", c.statuses...)
				noteVideosNow(t, run, catalog)
				seedMovie(t, catalog, "movie:tmdb:1002", "Another Survey (1999)", 300)

				runClosing(t, run, catalog, workerWebhooks)

				delivery := webhooksRun(t, catalog)
				if delivery.Failure != c.failure || delivery.Finished.IsZero() {
					t.Errorf("delivery = %+v, want a finished run with the failure %q", delivery, c.failure)
				}
				if got := receiver.requests(); got != c.requests {
					t.Errorf("requests = %d, want %d", got, c.requests)
				}
			})
		})
	}
}

// An address that never answers is tried for webhookDeadline and no longer,
// because the Job holds the Library's catalog claim while it tries.
func TestAnAddressThatNeverAnswersIsTriedForTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		run, _ := postingJob(t, catalog, "", http.StatusBadGateway)
		noteVideosNow(t, run, catalog)
		seedMovie(t, catalog, "movie:tmdb:1002", "Another Survey (1999)", 300)
		started := time.Now()
		done := make(chan error, 1)
		go func() { done <- run.runJob(t.Context()) }()
		confirmTheRun(t, catalog, workerEnrich, run.job)
		time.Sleep(webhookDeadline + webhookLongestBackoff)
		confirmTheRun(t, catalog, workerWebhooks, run.job)
		if err := <-done; err != nil {
			t.Fatalf("the job failed: %v", err)
		}

		delivery := webhooksRun(t, catalog)
		if took := delivery.Finished.Sub(started); took < webhookDeadline || took > webhookDeadline+webhookLongestBackoff {
			t.Errorf("the delivery took %s, want about %s", took, webhookDeadline)
		}
		if delivery.Failure != "webhook 1 (receiver.test): the receiver answered 502 Bad Gateway" {
			t.Errorf("failure = %q, want the last answer", delivery.Failure)
		}
	})
}

// The delivery's runs row.
func webhooksRun(t *testing.T, catalog *Catalog) libraryRun {
	t.Helper()
	runs, err := catalog.Runs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	delivery, held := runOf(runs["house/movies"], workerWebhooks)
	if !held {
		t.Fatal("the job wrote no runs row for the webhooks")
	}
	return delivery
}

// A Job whose scan noted nothing, such as a first walk, and a walk that
// changed no video post nothing and write no row of the webhooks worker.
func TestAJobPostsNothingWhenItHasNothingToReport(t *testing.T) {
	cases := []struct {
		name string
		note bool
	}{
		{"a scan that noted nothing", false},
		{"a walk that changed no video", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, _ := newSQLiteCatalog(t)
				seedMovie(t, catalog, "movie:tmdb:1001", "The Long Survey (1999)", 700)
				run, receiver := postingJob(t, catalog, "", http.StatusOK)
				if c.note {
					noteVideosNow(t, run, catalog)
				}

				runClosing(t, run, catalog)

				if got := receiver.requests(); got != 0 {
					t.Errorf("requests = %d, want none", got)
				}
				runs, err := catalog.Runs(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, held := runOf(runs["house/movies"], workerWebhooks); held {
					t.Error("the job wrote a runs row for webhooks it did not post")
				}
			})
		})
	}
}

// A Library with webhooks.
func webhookMovies() *Library {
	library := studioMovies()
	library.Spec.Webhooks = []LibraryWebhook{
		{URL: "http://plain.test/hook"},
		{URL: "http://checked.test/hook", SecretRef: &SecretKeyRef{Name: "hook-token"}},
	}
	return library
}

// The scan of a walk notes the videos after it syncs, and the close container
// takes the addresses, and each token through a secretKeyRef, so no container
// reads the API server.
func TestAWalkOfALibraryWithWebhooksTakesTheirEnvironment(t *testing.T) {
	plan := libraryJob{mode: jobModeWalk, sync: syncTarget{actor: "actor-1", version: 7}}

	job := buildLibraryJob(webhookMovies(), providerSet{}, nil, plan, testJobImages, testJobBus, testNow)

	scan := envOf(*jobContainer(job, scanPhase))
	if scan[libraryNoteVideosVariable] == "" || scan[syncActorVariable] != "actor-1" || scan[syncVersionVariable] != "7" {
		t.Errorf("scan env = %v, want the note and the sync target", scan)
	}
	closing := jobContainer(job, closeMode)
	if got := envOf(*closing)[libraryWebhooksVariable]; got != `["http://plain.test/hook","http://checked.test/hook"]` {
		t.Errorf("%s = %s, want both addresses in order", libraryWebhooksVariable, got)
	}
	var tokens []string
	for _, variable := range closing.Env {
		if variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil &&
			variable.ValueFrom.SecretKeyRef.Name == "hook-token" {
			tokens = append(tokens, variable.Name+"="+variable.ValueFrom.SecretKeyRef.Key)
		}
	}
	if want := []string{webhookTokenVariable(1) + "=token"}; !slices.Equal(tokens, want) {
		t.Errorf("tokens = %v, want %v", tokens, want)
	}
}

// A Job that fills gaps walks nothing and changes no video, so it posts
// nothing and takes no address.
func TestAGapJobTakesNoWebhook(t *testing.T) {
	library := webhookMovies()
	plan := libraryJob{mode: jobModeGaps, phases: servedPhases(library, providerSet{})[:1]}

	job := buildLibraryJob(library, providerSet{}, nil, plan, testJobImages, testJobBus, testNow)

	if got, held := envOf(*jobContainer(job, closeMode))[libraryWebhooksVariable]; held {
		t.Errorf("%s = %s, want none on a Job that does not walk", libraryWebhooksVariable, got)
	}
}

// The close container reads each address and the token of each one that
// names a Secret.
func TestTheCloseContainerReadsItsWebhooks(t *testing.T) {
	env := map[string]string{
		libraryWebhooksVariable: `["http://plain.test/hook","http://checked.test/hook"]`,
		webhookTokenVariable(1): "s3cret",
	}

	poster := webhookPosterOf(func(name string) string { return env[name] })

	want := []webhookTarget{
		{position: 0, url: "http://plain.test/hook"},
		{position: 1, url: "http://checked.test/hook", token: "s3cret"},
	}
	if !slices.Equal(poster.targets, want) {
		t.Errorf("targets = %+v, want %+v", poster.targets, want)
	}
}

// The log line of a delivery keeps its counts and names no title, no path,
// and no address, by the rule logline.go states.
func TestADeliveryLogsNoTitleAndNoAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		run, _ := postingJob(t, catalog, "", http.StatusUnauthorized)
		noteVideosNow(t, run, catalog)
		seedMovie(t, catalog, "movie:tmdb:1002", "Another Survey (1999)", 300)

		runClosing(t, run, catalog, workerWebhooks)

		wantOpaque(t, run.log.(*bytes.Buffer).String(), []string{"posted 1 title to 1 webhook, and 1 refused it"},
			[]string{"Another Survey", "receiver.test"})
	})
}

// An address list the close container cannot read is no address, so the Job
// posts nothing rather than guess.
func TestTheCloseContainerReadsNoWebhookFromAListItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		list string
	}{
		{"no list", ""},
		{"a list that is not JSON", "http://plain.test/hook"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			poster := webhookPosterOf(func(name string) string {
				if name == libraryWebhooksVariable {
					return c.list
				}
				return ""
			})

			if len(poster.targets) != 0 {
				t.Errorf("targets = %+v, want none", poster.targets)
			}
		})
	}
}

// A failure names its address by its position and its host, and never by its
// path, which can carry a key.
func TestAFailureNamesTheAddressByItsHost(t *testing.T) {
	cases := []struct {
		name    string
		address string
		want    string
	}{
		{"an address", "http://receiver.test/hook?key=s3cret", "receiver.test"},
		{"an address with no host", "/hook", "no host"},
		{"no URL at all", "http://[::1", "no host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hostOf(c.address); got != c.want {
				t.Errorf("host = %q, want %q", got, c.want)
			}
		})
	}
}

// A request that never reaches a receiver is tried again until the deadline,
// and one whose address is not a URL is not tried again.
func TestARequestThatReachesNoReceiverNamesWhy(t *testing.T) {
	cases := []struct {
		name    string
		address string
		want    string
	}{
		{"a receiver that is down", "http://receiver.test/hook", "webhook 1 (receiver.test): sending the request: "},
		{"an address that is not a URL", "http://[::1", "webhook 1 (no host): the address is not a URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := apiservertest.Start(t, &fakeReceiver{statuses: []int{http.StatusOK}})
				server.SetDown(true)
				poster := webhookPoster{targets: []webhookTarget{{url: c.address}}, client: server.Client()}

				failures := poster.postAll(t.Context(), []byte(`{}`))

				if len(failures) != 1 || !strings.HasPrefix(failures[0], c.want) {
					t.Errorf("failures = %q, want one that starts %q", failures, c.want)
				}
			})
		})
	}
}

// A walk of the folders that webhooks named says so, so a receiver can tell an
// import from the hourly walk.
func TestAWalkOfFoldersSaysSo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		run, receiver := postingJob(t, catalog, "", http.StatusOK)
		run.scanPaths = []string{"/media/movies/Another Survey (1999)"}
		noteVideosNow(t, run, catalog)
		seedMovie(t, catalog, "movie:tmdb:1002", "Another Survey (1999)", 300)

		runClosing(t, run, catalog, workerWebhooks)

		if walk := receiver.lastBody(t).Walk; walk != walkFolders {
			t.Errorf("walk = %q, want %q", walk, walkFolders)
		}
	})
}
