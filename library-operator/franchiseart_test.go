package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// linkedFranchiseFile is a franchise directory that carries its art as links,
// the way the public repository does. The five keys are Kodi's names, and each
// is an https URL.
const linkedFranchiseFile = `name: Alien
art:
  poster: https://art.example/alien/poster.jpg
  fanart: https://art.example/alien/fanart.jpg
  landscape: https://art.example/alien/landscape.jpg
  logo: https://art.example/alien/logo.png
  banner: https://art.example/alien/banner.jpg
order:
  - movie: tmdb:348
`

// artHost is an art host that answers every path with one image, and counts
// what it was asked for. The type it answers is what names the file the fetch
// writes, so a case drives a png by asking for one.
type artHost struct {
	server *httptest.Server

	mutex sync.Mutex
	asked []string
	// agent is the User-Agent the last request carried.
	agent string
	// status is what the host answers, and 200 by default.
	status int
	// kind is the content type the host answers with.
	kind string
	// body is the image the host answers with.
	body string
}

func newArtHost(t *testing.T) *artHost {
	t.Helper()
	host := &artHost{status: http.StatusOK, kind: "image/jpeg", body: "the image"}
	// The links are https, which the file's own schema requires, so the host
	// serves TLS and answers through the client httptest builds for it.
	host.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host.mutex.Lock()
		host.asked = append(host.asked, r.URL.Path)
		host.agent = r.Header.Get("User-Agent")
		status, kind, body := host.status, host.kind, host.body
		host.mutex.Unlock()
		w.Header().Set("Content-Type", kind)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(host.server.Close)
	return host
}

// reads are the paths the host was asked for since the last read.
func (h *artHost) reads() []string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	held := slices.Clone(h.asked)
	h.asked = nil
	return held
}

// file is a franchise.yaml whose art links point at this host.
func (h *artHost) file(keys ...string) string {
	body := "name: Alien\n"
	if len(keys) > 0 {
		body += "art:\n"
	}
	for _, key := range keys {
		body += "  " + key + ": " + h.server.URL + "/alien/" + key + "\n"
	}
	return body + "order:\n  - movie: tmdb:348\n"
}

// artFetchOf is one scan Job's art fetch over a checkout and a claim of its
// own. It writes through the same door every write to a volume takes.
func artFetchOf(t *testing.T, host *artHost, files map[string]string) (franchiseArtFetch, string, string) {
	t.Helper()
	checkout := franchiseCheckout(t, files)
	claim := t.TempDir()
	fetch := franchiseArtFetch{
		client: host.server.Client(),
		writer: newVolumeWriter("scan-1"),
		log:    func(format string, args ...any) { t.Logf(format, args...) },
	}
	return fetch, checkout, claim
}

// filesIn are the files one directory of the claim holds, in name order.
func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

// The fetch writes one file per link, under the names plan 30's art facts
// write, and records the link each one came from. The extension comes from
// what the host answered and never from the URL.
func TestTheArtFetchWritesEveryLinkUnderItsKodiName(t *testing.T) {
	host := newArtHost(t)
	fetch, checkout, claim := artFetchOf(t, host,
		map[string]string{"Alien/franchise.yaml": host.file("poster", "fanart", "landscape", "logo", "banner")})

	wrote := fetch.fetchAll(t.Context(), checkout, claim)

	if wrote != 5 {
		t.Errorf("the fetch wrote %d files, want the five links", wrote)
	}
	// The logo lands as clearlogo, which is the name plan 30's logo fact
	// writes, and its extension is the jpeg the host answered and not the png
	// the url names.
	want := []string{".liken", "banner.jpg", "clearlogo.jpg", "fanart.jpg", "landscape.jpg", "poster.jpg"}
	if held := filesIn(t, filepath.Join(claim, "Alien")); !slices.Equal(held, want) {
		t.Errorf("the claim holds %v, want %v", held, want)
	}
	ledger, err := readLikenLedger(filepath.Join(claim, "Alien"), franchiseArtFact)
	if err != nil {
		t.Fatal(err)
	}
	item, held := ledger.itemAt("poster")
	if !held || item.Source != host.server.URL+"/alien/poster" {
		t.Errorf("the ledger holds %+v, want the link the poster came from", item)
	}
}

// The fetch names itself to the host, because Wikimedia refuses a request
// that carries a generic client name.
func TestTheArtFetchNamesItselfToTheHost(t *testing.T) {
	host := newArtHost(t)
	fetch, checkout, claim := artFetchOf(t, host,
		map[string]string{"Alien/franchise.yaml": host.file("poster")})

	fetch.fetchAll(t.Context(), checkout, claim)

	host.mutex.Lock()
	defer host.mutex.Unlock()
	if host.agent != franchiseArtUserAgent {
		t.Errorf("the host saw the agent %q, want %q", host.agent, franchiseArtUserAgent)
	}
}

// A link the last scan already read is not read again, so a scan on a schedule
// reaches the network only for art it does not hold.
func TestTheArtFetchReadsALinkOnce(t *testing.T) {
	host := newArtHost(t)
	fetch, checkout, claim := artFetchOf(t, host,
		map[string]string{"Alien/franchise.yaml": host.file("poster")})

	if wrote := fetch.fetchAll(t.Context(), checkout, claim); wrote != 1 {
		t.Fatalf("the first fetch wrote %d files, want the one link", wrote)
	}
	host.reads()
	wrote := fetch.fetchAll(t.Context(), checkout, claim)

	if wrote != 0 {
		t.Errorf("the second fetch wrote %d files, want none", wrote)
	}
	if reads := host.reads(); len(reads) != 0 {
		t.Errorf("the second fetch read %v, want nothing", reads)
	}
}

// A link the author changed is read again, and the file it names is written
// over, because the file is this fetch's own.
func TestTheArtFetchReadsAChangedLinkAgain(t *testing.T) {
	host := newArtHost(t)
	files := map[string]string{"Alien/franchise.yaml": host.file("poster")}
	fetch, checkout, claim := artFetchOf(t, host, files)
	if wrote := fetch.fetchAll(t.Context(), checkout, claim); wrote != 1 {
		t.Fatalf("the first fetch wrote %d files, want the one link", wrote)
	}

	host.body = "the second image"
	changed := strings.Replace(host.file("poster"), "/alien/poster", "/alien/poster-2", 1)
	if err := os.WriteFile(filepath.Join(checkout, "Alien", franchiseFileName),
		[]byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote := fetch.fetchAll(t.Context(), checkout, claim)

	if wrote != 1 {
		t.Errorf("the fetch wrote %d files, want the changed link", wrote)
	}
	held, err := os.ReadFile(filepath.Join(claim, "Alien", "poster.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(held) != "the second image" {
		t.Errorf("the poster holds %q, want the image the changed link answered", held)
	}
}

// A file the ledger does not name is the owner's, and the fetch never writes
// over it. A fork that carries its own art carries it on purpose.
func TestTheArtFetchKeepsAFileItDidNotWrite(t *testing.T) {
	host := newArtHost(t)
	fetch, checkout, claim := artFetchOf(t, host,
		map[string]string{"Alien/franchise.yaml": host.file("poster")})
	if err := os.MkdirAll(filepath.Join(claim, "Alien"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claim, "Alien", "poster.jpg"),
		[]byte("the owner's poster"), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote := fetch.fetchAll(t.Context(), checkout, claim)

	if wrote != 0 {
		t.Errorf("the fetch wrote %d files, want it to keep the owner's", wrote)
	}
	held, err := os.ReadFile(filepath.Join(claim, "Alien", "poster.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(held) != "the owner's poster" {
		t.Errorf("the poster holds %q, want the owner's own", held)
	}
	if reads := host.reads(); len(reads) != 0 {
		t.Errorf("the fetch read %v, want nothing", reads)
	}
}

// A link that fails leaves no file, and the scan carries on. The walk that
// follows still writes its rows, so a host that is down for an hour costs the
// art and never the order.
func TestTheArtFetchLeavesNoFileForALinkThatFails(t *testing.T) {
	cases := []struct {
		name   string
		status int
		kind   string
	}{
		{"a link the host refuses", http.StatusNotFound, "image/jpeg"},
		{"an answer that is not an image", http.StatusOK, "text/html"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			host := newArtHost(t)
			host.status, host.kind = testCase.status, testCase.kind
			fetch, checkout, claim := artFetchOf(t, host,
				map[string]string{"Alien/franchise.yaml": host.file("poster", "fanart")})

			wrote := fetch.fetchAll(t.Context(), checkout, claim)

			if wrote != 0 {
				t.Errorf("the fetch wrote %d files, want none", wrote)
			}
			if held := filesIn(t, filepath.Join(claim, "Alien")); len(held) != 0 {
				t.Errorf("the claim holds %v, want nothing", held)
			}
			result := walkFranchises(checkout, claim, "house/franchises")
			if len(result.franchises) != 1 || result.readError {
				t.Errorf("the walk wrote %+v with readError %v, want the row and a complete pass",
					result.franchises, result.readError)
			}
		})
	}
}

// An answer over the size cap is refused, so one link cannot fill the claim.
func TestTheArtFetchRefusesAnAnswerOverTheCap(t *testing.T) {
	host := newArtHost(t)
	host.body = "0123456789"
	was := franchiseArtSizeCap
	franchiseArtSizeCap = 4
	t.Cleanup(func() { franchiseArtSizeCap = was })
	fetch, checkout, claim := artFetchOf(t, host,
		map[string]string{"Alien/franchise.yaml": host.file("poster")})

	if wrote := fetch.fetchAll(t.Context(), checkout, claim); wrote != 0 {
		t.Errorf("the fetch wrote %d files, want none over the cap", wrote)
	}
	if held := filesIn(t, filepath.Join(claim, "Alien")); len(held) != 0 {
		t.Errorf("the claim holds %v, want nothing", held)
	}
}

// The row reads its art off the claim, so art and arts hold paths under the
// library root and never links. A png answer writes a png, and the row names
// it.
func TestTheRowReadsTheArtTheFetchWroteOntoTheClaim(t *testing.T) {
	host := newArtHost(t)
	host.kind = "image/png"
	fetch, checkout, claim := artFetchOf(t, host,
		map[string]string{"Alien/franchise.yaml": host.file("poster", "fanart")})

	fetch.fetchAll(t.Context(), checkout, claim)
	result := walkFranchises(checkout, claim, "house/franchises")

	if len(result.franchises) != 1 {
		t.Fatalf("the walk wrote %d rows, want the one directory", len(result.franchises))
	}
	row := result.franchises[0]
	if row.Art != filepath.Join("Alien", "poster.png") {
		t.Errorf("art = %q, want the poster on the claim", row.Art)
	}
	want := []string{filepath.Join("Alien", "poster.png"), filepath.Join("Alien", "fanart.png")}
	if !slices.Equal(row.Arts, want) {
		t.Errorf("arts = %v, want %v", row.Arts, want)
	}
}

// A franchise whose directory the fetch has not written holds no art, and that
// is an answer and never a read the walk failed.
func TestTheWalkReadsNoArtForADirectoryTheClaimDoesNotHold(t *testing.T) {
	result := walkFranchises(franchiseCheckout(t,
		map[string]string{"Alien/franchise.yaml": linkedFranchiseFile}),
		t.TempDir(), "house/franchises")

	if len(result.franchises) != 1 {
		t.Fatalf("the walk wrote %d rows, want the one directory", len(result.franchises))
	}
	if result.franchises[0].Art != "" || result.readError {
		t.Errorf("art = %q with readError %v, want none and a complete pass",
			result.franchises[0].Art, result.readError)
	}
}
