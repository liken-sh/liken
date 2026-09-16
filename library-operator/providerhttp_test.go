package main

// What these tests read: the slot every request of the shared layer takes
// before it goes, and the interval each provider block holds to.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// No test paces unless it says so, so a fake provider answers at once and no
// test sleeps out a real interval.
func TestMain(m *testing.M) {
	providerPaceFor = func(string) time.Duration { return 0 }
	os.Exit(m.Run())
}

// The real table, for the length of one test.
func paceFromTheTable(t *testing.T) {
	t.Helper()
	held := providerPaceFor
	providerPaceFor = func(block string) time.Duration { return blockOf(block).pace }
	t.Cleanup(func() { providerPaceFor = held })
}

// What one fake provider recorded: the path of every request it was asked.
type fakeProvider struct {
	mutex    sync.Mutex
	requests []string
}

func (f *fakeProvider) read() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.requests
}

// The request layer and the fake provider it reads. The answer is the one
// this request takes, counted from one.
func newFakeProvider(t *testing.T, answer func(int) (int, string)) (*providerRequests, *fakeProvider) {
	t.Helper()
	fake := &fakeProvider{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mutex.Lock()
		fake.requests = append(fake.requests, r.URL.Path)
		count := len(fake.requests)
		fake.mutex.Unlock()
		status, body := answer(count)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	requests := newProviderRequests(providerBlockTMDb, server.URL, nil)
	requests.http = server.Client()
	return &requests, fake
}

// An answer every call of these tests reads.
func anAnswer(int) (int, string) { return http.StatusOK, `{"held":true}` }

// Every provider call takes the next slot, so two calls of one client are an
// interval apart, and a client with no pace sends both at once.
func TestEveryProviderCallTakesItsSlot(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		least    time.Duration
		most     time.Duration
	}{
		{name: "a client with no pace sends both at once",
			interval: 0, most: 50 * time.Millisecond},
		{name: "a client with a pace waits the interval out",
			interval: 50 * time.Millisecond, least: 50 * time.Millisecond, most: time.Minute},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			requests, _ := newFakeProvider(t, anAnswer)
			requests.interval = one.interval
			answer := map[string]any{}
			started := time.Now()

			first := requests.get(t.Context(), "/one", nil, &answer)
			second := requests.get(t.Context(), "/two", nil, &answer)

			took := time.Since(started)
			if first != nil || second != nil {
				t.Fatalf("the calls read %v and %v", first, second)
			}
			if took < one.least || took > one.most {
				t.Errorf("two calls took %v, want between %v and %v", took, one.least, one.most)
			}
		})
	}
}

// A file the provider serves takes a slot of the same pace, because the art
// the answerers read leaves by the same layer.
func TestEveryProviderFileTakesItsSlot(t *testing.T) {
	requests, fake := newFakeProvider(t, func(int) (int, string) {
		return http.StatusOK, "the image"
	})
	requests.interval = 50 * time.Millisecond
	started := time.Now()

	_, first := requests.fetchFile(t.Context(), requests.base+"/one.jpg")
	_, second := requests.fetchFile(t.Context(), requests.base+"/two.jpg")

	took := time.Since(started)
	if first != nil || second != nil {
		t.Fatalf("the fetches read %v and %v", first, second)
	}
	if took < 50*time.Millisecond {
		t.Errorf("two fetches took %v, want at least the interval", took)
	}
	if len(fake.read()) != 2 {
		t.Errorf("the provider was asked %v, want both files", fake.read())
	}
}

// A container that is told to stop while it waits for a slot reads the
// context's own error, and the request it held never goes.
func TestARequestThatCannotTakeItsSlotSendsNothing(t *testing.T) {
	requests, fake := newFakeProvider(t, anAnswer)
	requests.interval = time.Minute
	answer := map[string]any{}
	ctx, stop := context.WithCancel(t.Context())
	t.Cleanup(stop)

	if err := requests.get(ctx, "/one", nil, &answer); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(30*time.Millisecond, stop)
	err := requests.get(ctx, "/two", nil, &answer)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("the second call read %v, want the context's own error", err)
	}
	if got := fake.read(); len(got) != 1 {
		t.Errorf("the provider was asked %v, want the one request that went before the wait", got)
	}
}

// The attempt a 429 sends again takes a slot of its own, so a cooldown that
// ends early does not send two requests at once.
func TestARetryAfterACooldownWaitsForItsSlot(t *testing.T) {
	requests, fake := newFakeProvider(t, func(count int) (int, string) {
		if count == 1 {
			return http.StatusTooManyRequests, ""
		}
		return http.StatusOK, `{"held":true}`
	})
	requests.interval = 50 * time.Millisecond
	requests.wait = func(context.Context, time.Duration) error { return nil }
	answer := map[string]any{}
	started := time.Now()

	err := requests.get(t.Context(), "/one", nil, &answer)

	took := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.read()) != 2 {
		t.Errorf("the provider was asked %v, want the request and the retry", fake.read())
	}
	if took < 50*time.Millisecond {
		t.Errorf("the retry went after %v, want at least the interval", took)
	}
}

// The interval each block holds to, and a block the table states none for,
// which paces at none.
func TestThePaceEachProviderBlockHoldsTo(t *testing.T) {
	cases := []struct {
		block string
		want  time.Duration
	}{
		{block: providerBlockTMDb, want: 50 * time.Millisecond},
		{block: providerBlockOMDb, want: 100 * time.Millisecond},
		{block: providerBlockFanart, want: 100 * time.Millisecond},
		{block: providerBlockTVmaze, want: 500 * time.Millisecond},
		{block: providerBlockPeerTube, want: 250 * time.Millisecond},
		{block: providerBlockArchive, want: 250 * time.Millisecond},
		{block: "a block of no pace", want: 0},
	}
	for _, one := range cases {
		t.Run(one.block, func(t *testing.T) {
			paceFromTheTable(t)

			requests := newProviderRequests(one.block, "https://example.test", nil)

			if requests.interval != one.want {
				t.Errorf("the %s block paces at %v, want %v", one.block, requests.interval, one.want)
			}
		})
	}
}

// Every request counts under the provider that answered and the class of its
// answer, so a provider that starts refusing is a line on a graph.
func TestEveryRequestCountsUnderItsStatusClass(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		class    string
		requests float64
	}{
		{name: "an answer", status: http.StatusOK, class: "2xx", requests: 1},
		{name: "a provider that asks for a slower pace",
			status: http.StatusTooManyRequests, class: "429", requests: providerAttempts},
		{name: "a key the provider refused", status: http.StatusUnauthorized, class: "4xx", requests: 1},
		{name: "a provider that is down", status: http.StatusBadGateway, class: "5xx", requests: 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			requests, _ := newFakeProvider(t, func(int) (int, string) { return one.status, `{}` })
			requests.wait = func(context.Context, time.Duration) error { return nil }
			record := newTallies(nil, "house/movies", workerEnrich, "enrich-1", nfoContainerName, time.Now())
			requests.recordTo(record)

			answer := map[string]any{}
			_ = requests.get(t.Context(), "/one", nil, &answer)

			held := record.totals()
			counted := tallyCell{metric: tallyProviderRequests,
				labels: "provider=tmdb,status=" + one.class}
			if held[counted] != one.requests {
				t.Errorf("%v = %v, want %v", counted, held[counted], one.requests)
			}
			seconds := tallyCell{metric: tallyProviderRequestSeconds, labels: "provider=tmdb"}
			if _, timed := held[seconds]; !timed {
				t.Errorf("the recorder holds %v, want the seconds of every request", held)
			}
		})
	}
}

// A request that never got an answer counts as an error, because a provider
// that cannot be reached is the same fault to a reader.
func TestARequestWithNoAnswerCountsAsAnError(t *testing.T) {
	requests := newProviderRequests(providerBlockTMDb, "http://127.0.0.1:1", nil)
	record := newTallies(nil, "house/movies", workerEnrich, "enrich-1", nfoContainerName, time.Now())
	requests.recordTo(record)

	answer := map[string]any{}
	err := requests.get(t.Context(), "/one", nil, &answer)

	if err == nil {
		t.Fatal("the request answered no error, want the refused connection")
	}
	held := record.totals()
	counted := tallyCell{metric: tallyProviderRequests, labels: "provider=tmdb,status=error"}
	if held[counted] != 1 {
		t.Errorf("%v = %v, want 1", counted, held[counted])
	}
}

// A file fetch counts the same way, because both sends share one round trip.
func TestAFileFetchCountsItsRequest(t *testing.T) {
	requests, _ := newFakeProvider(t, func(int) (int, string) { return http.StatusOK, "a picture" })
	record := newTallies(nil, "house/movies", workerEnrich, "enrich-1", nfoContainerName, time.Now())
	requests.recordTo(record)

	if _, err := requests.fetchFile(t.Context(), requests.base+"/poster.jpg"); err != nil {
		t.Fatal(err)
	}

	held := record.totals()
	counted := tallyCell{metric: tallyProviderRequests, labels: "provider=tmdb,status=2xx"}
	if held[counted] != 1 {
		t.Errorf("%v = %v, want 1", counted, held[counted])
	}
}

// A status outside the classes counts as an error. The operator's own
// provider check never reaches this, because it names no recorder.
func TestAStatusOutsideTheClassesCountsAsAnError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   string
	}{
		{name: "a redirect the client did not follow", status: http.StatusMultipleChoices, want: "error"},
		{name: "an answer", status: http.StatusNoContent, want: "2xx"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := providerStatusClass(one.status); got != one.want {
				t.Errorf("providerStatusClass(%d) = %q, want %q", one.status, got, one.want)
			}
		})
	}
}
