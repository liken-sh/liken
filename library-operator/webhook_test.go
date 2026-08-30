package main

// These tests prove the import webhook: the path it reads
// out of a Radarr, Sonarr, or Jellyfin payload, the way it maps that
// path onto the volume, and the rescan a POST drives into the catalog.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// testScanner builds a scanner over a fixture root with a recording
// catalog and a bus that never connects, so the walk and the webhook run
// with no cluster and no broker.
func testScanner(t *testing.T, root, kind string) (*scanner, *catalogRecorder) {
	t.Helper()
	catalog, recorder := recordingCatalog(t)
	scan := &scanner{
		root:    root,
		library: "house/library",
		kind:    kind,
		catalog: catalog,
		bus:     newBus("", "test", nil, nil, nil),
	}
	return scan, recorder
}

func TestExtractWebhookPath(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "radarr prefers the file path",
			body: `{"movie":{"folderPath":"/movies/X"},"movieFile":{"path":"/movies/X/x.mkv"}}`,
			want: "/movies/X/x.mkv",
		},
		{
			name: "radarr folder path when there is no file",
			body: `{"movie":{"folderPath":"/movies/X"}}`,
			want: "/movies/X",
		},
		{
			name: "sonarr episode file path",
			body: `{"series":{"path":"/tv/Show"},"episodeFile":{"path":"/tv/Show/S01E01.mkv"}}`,
			want: "/tv/Show/S01E01.mkv",
		},
		{
			name: "sonarr series path when there is no file",
			body: `{"series":{"path":"/tv/Show"}}`,
			want: "/tv/Show",
		},
		{
			name: "jellyfin top-level Path",
			body: `{"ItemType":"Movie","Path":"/movies/X/x.mkv"}`,
			want: "/movies/X/x.mkv",
		},
		{
			name: "no path in the payload",
			body: `{"eventType":"Test"}`,
			want: "",
		},
		{
			name: "a non-string path field is ignored",
			body: `{"movieFile":{"path":42}}`,
			want: "",
		},
		{
			name: "malformed json",
			body: `{not json`,
			want: "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := extractWebhookPath([]byte(testCase.body)); got != testCase.want {
				t.Errorf("extractWebhookPath = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestResolveWebhookPath(t *testing.T) {
	scan, _ := testScanner(t, "testdata/movies", libraryKindMovies)
	matrix := filepath.Join("testdata", "movies", "Action", "The Matrix (1999)")
	matrixFile := filepath.Join(matrix, "The Matrix (1999).mkv")

	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "an absolute server path matches by its suffix",
			payload: "/data/media/Action/The Matrix (1999)/The Matrix (1999).mkv",
			want:    matrixFile,
		},
		{
			name:    "a relative path joins the root",
			payload: "Action/The Matrix (1999)",
			want:    matrix,
		},
		{
			name:    "a path that maps to nothing resolves empty",
			payload: "/data/media/Nowhere/thing.mkv",
			want:    "",
		},
		{
			name:    "an empty path resolves empty",
			payload: "",
			want:    "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := scan.resolveWebhookPath(testCase.payload); got != testCase.want {
				t.Errorf("resolveWebhookPath(%q) = %q, want %q", testCase.payload, got, testCase.want)
			}
		})
	}
}

func TestWebhookRescansTheNamedPath(t *testing.T) {
	scan, recorder := testScanner(t, "testdata/movies", libraryKindMovies)
	server := httptest.NewServer(scan.webhookHandler())
	t.Cleanup(server.Close)

	body := `{"movie":{"folderPath":"/srv/media/Action/The Matrix (1999)"}}`
	response, err := http.Post(server.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", response.StatusCode)
	}
	if !containsKind(sqlKinds(recorder), "INSERT MOVIES") {
		t.Errorf("the webhook wrote no movie row: %v", sqlKinds(recorder))
	}
	if !postedWith(recorder, "movie:tmdb:603") {
		t.Error("the rescan did not upsert The Matrix")
	}
	_ = scan
}

func TestWebhookFallsBackToAFullWalk(t *testing.T) {
	scan, recorder := testScanner(t, "testdata/movies", libraryKindMovies)
	server := httptest.NewServer(scan.webhookHandler())
	t.Cleanup(server.Close)

	// A payload that maps to no path on the volume drives a full walk,
	// so every title is written and not just one.
	body := `{"movie":{"folderPath":"/srv/media/Nowhere"}}`
	response, err := http.Post(server.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if scan.report.Titles != 3 {
		t.Errorf("titles = %d, want a full walk of all three", scan.report.Titles)
	}
	_ = recorder
}

func TestWebhookRejectsGet(t *testing.T) {
	scan, recorder := testScanner(t, "testdata/movies", libraryKindMovies)
	server := httptest.NewServer(scan.webhookHandler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", response.StatusCode)
	}
	if len(recorder.all()) != 0 {
		t.Error("a GET wrote to the catalog")
	}
}

func TestExtractWebhookPathIgnoresANonObjectField(t *testing.T) {
	if got := extractWebhookPath([]byte(`{"movie":5}`)); got != "" {
		t.Errorf("extractWebhookPath = %q, want empty when the nested field is not an object", got)
	}
}

func TestResolveWebhookPathRelativeMissing(t *testing.T) {
	scan, _ := testScanner(t, "testdata/movies", libraryKindMovies)
	if got := scan.resolveWebhookPath("Action/Not There"); got != "" {
		t.Errorf("resolveWebhookPath = %q, want empty for a missing relative path", got)
	}
}
