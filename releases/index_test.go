package releases

// Tests for the channel's index pages. Like the fetch tests, these
// round-trip through the package's own machinery: Bundle lays out
// real (tiny) releases, the serve handler publishes them, and Index
// reads them back over HTTP the way it reads the public channel. So
// no fixture here can describe a channel that Bundle would not
// produce.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
	"sigs.k8s.io/yaml"
)

// servedReleases bundles several versions into one channel and serves
// it. It returns the source URL, the channel directory, and the key
// list that the channel's object storage would report for it.
func servedReleases(t *testing.T, versions ...string) (string, string, []string) {
	t.Helper()
	source, channel := servedChannel(t, versions[0])
	var keys []string
	for _, version := range versions[1:] {
		rebundleInto(t, channel, version)
	}
	for _, version := range versions {
		keys = append(keys, version+"/release.yaml", version+"/vmlinuz")
	}
	return source, channel, append(keys, "channel.yaml", "favicon.ico", "index.html")
}

// pageAt reads one rendered page out of the output directory.
func pageAt(t *testing.T, dir string, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{dir}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// indexed renders a channel holding the given versions and returns the
// output directory.
func indexed(t *testing.T, versions ...string) (string, string) {
	t.Helper()
	source, channel, keys := servedReleases(t, versions...)
	dir := t.TempDir()
	var out bytes.Buffer
	if err := Index(source, keys, dir, &out); err != nil {
		t.Fatal(err)
	}
	return dir, channel
}

func TestIndexListsEveryReleaseNewestFirst(t *testing.T) {
	dir, _ := indexed(t, "2026.07.14-001", "2026.07.14-002", "2026.07.15-001")

	page := pageAt(t, dir, "index.html")
	newest := strings.Index(page, "2026.07.15-001")
	middle := strings.Index(page, "2026.07.14-002")
	oldest := strings.Index(page, "2026.07.14-001")
	if newest < 0 || middle < 0 || oldest < 0 {
		t.Fatalf("the front page does not name every release:\n%s", page)
	}
	if !(newest < middle && middle < oldest) {
		t.Errorf("the front page lists releases oldest first:\n%s", page)
	}
}

func TestIndexMarksTheReleaseTheChannelDocumentNames(t *testing.T) {
	dir, _ := indexed(t, "2026.07.14-001", "2026.07.15-001")

	page := pageAt(t, dir, "index.html")
	if !strings.Contains(page, `<td class="latest"><a href="/2026.07.15-001/">2026.07.15-001</a></td>`) {
		t.Errorf("the front page does not mark the latest release:\n%s", page)
	}
	if strings.Contains(page, `<td class="latest"><a href="/2026.07.14-001/">`) {
		t.Errorf("the front page marks an older release as the latest:\n%s", page)
	}
}

func TestIndexWritesAPageForEachRelease(t *testing.T) {
	dir, _ := indexed(t, "2026.07.14-001", "2026.07.15-001")

	for _, version := range []string{"2026.07.14-001", "2026.07.15-001"} {
		page := pageAt(t, dir, version, "index.html")
		if !strings.Contains(page, version) {
			t.Errorf("%s's page does not name it:\n%s", version, page)
		}
	}
}

func TestIndexPrintsTheCatalogEntry(t *testing.T) {
	dir, channel := indexed(t, "2026.07.15-001")

	// The digest a page prints is the one an operator pastes into a
	// Cluster's spec.releases.catalog, so it must be the document's own
	// bytes, exactly as the release workflow reports them.
	page := pageAt(t, dir, "2026.07.15-001", "index.html")
	digest := documentDigest(t, channel, "2026.07.15-001")
	if !strings.Contains(page, digest) {
		t.Errorf("the page does not print the document digest %s:\n%s", digest, page)
	}
	if !strings.Contains(page, "version: 2026.07.15-001") {
		t.Errorf("the page does not print a catalog entry:\n%s", page)
	}
}

func TestIndexNamesEveryArtifactWithItsDigest(t *testing.T) {
	dir, channel := indexed(t, "2026.07.15-001")

	page := pageAt(t, dir, "2026.07.15-001", "index.html")
	raw, err := os.ReadFile(filepath.Join(channel, "2026.07.15-001", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range release.Artifacts {
		if !strings.Contains(page, artifact.Name) {
			t.Errorf("the page does not name %s", artifact.Name)
		}
		if !strings.Contains(page, artifact.SHA256) {
			t.Errorf("the page does not give %s's digest", artifact.Name)
		}
	}
}

func TestIndexRecordsTheComponents(t *testing.T) {
	dir, _ := indexed(t, "2026.07.15-001")

	page := pageAt(t, dir, "2026.07.15-001", "index.html")
	if !strings.Contains(page, "kernel") || !strings.Contains(page, testComponents[0].Version) {
		t.Errorf("the page does not record the components:\n%s", page)
	}
}

func TestIndexLinksArtifactsFromTheRoot(t *testing.T) {
	dir, _ := indexed(t, "2026.07.15-001")

	// A release's page answers at both /<version>/ and /<version>, and
	// the second form has no trailing slash to resolve a relative link
	// against. Root-absolute links answer the same way at both.
	page := pageAt(t, dir, "2026.07.15-001", "index.html")
	if !strings.Contains(page, `href="/2026.07.15-001/vmlinuz"`) {
		t.Errorf("the page does not link vmlinuz from the root:\n%s", page)
	}
}

func TestIndexIgnoresKeysThatAreNotReleases(t *testing.T) {
	source, _, keys := servedReleases(t, "2026.07.15-001")
	dir := t.TempDir()

	var out bytes.Buffer
	if err := Index(source, append(keys, "robots.txt", "notes/scratch.txt"), dir, &out); err != nil {
		t.Fatal(err)
	}

	page := pageAt(t, dir, "index.html")
	if strings.Contains(page, "scratch") || strings.Contains(page, "robots") {
		t.Errorf("the front page lists something that is not a release:\n%s", page)
	}
}

func TestIndexGroupsTheSourceMirror(t *testing.T) {
	source, _, keys := servedReleases(t, "2026.07.15-001")
	keys = append(keys,
		"sources/kernel/7.1.2/linux-7.1.2.tar.xz",
		"sources/kernel/7.1.2/config",
		"sources/xtables/v0.15.2/iptables-1.8.11.tar.xz")
	dir := t.TempDir()

	var out bytes.Buffer
	if err := Index(source, keys, dir, &out); err != nil {
		t.Fatal(err)
	}

	page := pageAt(t, dir, "sources", "index.html")
	for _, want := range []string{"kernel", "7.1.2", "linux-7.1.2.tar.xz", "config",
		"xtables", `href="/sources/xtables/v0.15.2/iptables-1.8.11.tar.xz"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the sources page does not carry %q:\n%s", want, page)
		}
	}
}

func TestIndexWritesTheVersionsDocument(t *testing.T) {
	dir, channel := indexed(t, "2026.07.14-001", "2026.07.15-001")

	raw, err := os.ReadFile(filepath.Join(dir, "versions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document Versions
	if err := yaml.UnmarshalStrict(raw, &document); err != nil {
		t.Fatal(err)
	}

	if document.Kind != "Versions" || document.Latest != "2026.07.15-001" {
		t.Errorf("the document is %s naming %s as latest", document.Kind, document.Latest)
	}
	want := []VersionEntry{
		{Version: "2026.07.15-001", Digest: documentDigest(t, channel, "2026.07.15-001")},
		{Version: "2026.07.14-001", Digest: documentDigest(t, channel, "2026.07.14-001")},
	}
	if !slices.Equal(document.Releases, want) {
		t.Errorf("the document lists %v, not %v", document.Releases, want)
	}
}

func TestIndexWritesNoSourcesPageForAnEmptyMirror(t *testing.T) {
	dir, _ := indexed(t, "2026.07.15-001")

	if _, err := os.Stat(filepath.Join(dir, "sources", "index.html")); !os.IsNotExist(err) {
		t.Errorf("a channel with no source mirror got a sources page")
	}
}

func TestIndexRefusesAReleaseTheChannelDoesNotServe(t *testing.T) {
	source, _, keys := servedReleases(t, "2026.07.15-001")
	dir := t.TempDir()

	var out bytes.Buffer
	err := Index(source, append(keys, "2026.07.16-001/release.yaml"), dir, &out)
	if err == nil {
		t.Fatal("indexing accepted a version the channel does not serve")
	}
	if !strings.Contains(err.Error(), "2026.07.16-001") {
		t.Errorf("the error does not name the missing release: %v", err)
	}
}

func TestIndexIsIdempotent(t *testing.T) {
	source, _, keys := servedReleases(t, "2026.07.14-001", "2026.07.15-001")
	first, second := t.TempDir(), t.TempDir()

	var out bytes.Buffer
	if err := Index(source, keys, first, &out); err != nil {
		t.Fatal(err)
	}
	if err := Index(source, keys, second, &out); err != nil {
		t.Fatal(err)
	}

	// Nothing a page shows comes from the clock or from the order of a
	// map, so the same channel renders the same bytes every time. A
	// rerun therefore heals a damaged page and changes nothing else.
	for _, page := range []string{"index.html", "2026.07.15-001/index.html"} {
		if pageAt(t, first, page) != pageAt(t, second, page) {
			t.Errorf("%s differs between two runs over the same channel", page)
		}
	}
}

// notedRelease writes a notes object beside a bundled release, the
// way the release workflow uploads one, and returns the key that the
// channel's storage would report for it.
func notedRelease(t *testing.T, channel, version, notes string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(channel, version, "notes.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	return version + "/notes.md"
}

func TestIndexRendersTheNotesTheChannelServes(t *testing.T) {
	source, channel, keys := servedReleases(t, "2026.07.15-001")
	keys = append(keys, notedRelease(t, channel, "2026.07.15-001",
		"Changes since 2026.07.14-001:\n\n- Fix the <thing> nobody liked\n"))

	dir := t.TempDir()
	if err := Index(source, keys, dir, nil); err != nil {
		t.Fatal(err)
	}

	page := pageAt(t, dir, "2026.07.15-001", "index.html")
	if !strings.Contains(page, "What changed") {
		t.Errorf("the page has no notes section:\n%s", page)
	}
	// The notes are prose from a Markdown file, rendered as text: the
	// template must escape them, never trust them as markup.
	if !strings.Contains(page, "Fix the &lt;thing&gt; nobody liked") {
		t.Errorf("the page does not carry the notes, escaped:\n%s", page)
	}
}

func TestIndexOmitsTheNotesSectionWhenTheChannelHasNone(t *testing.T) {
	dir, _ := indexed(t, "2026.07.15-001")

	page := pageAt(t, dir, "2026.07.15-001", "index.html")
	if strings.Contains(page, "What changed") {
		t.Errorf("the page renders a notes section with no notes:\n%s", page)
	}
}

// A key list that names notes the channel does not serve is the same
// kind of disagreement as a release the channel does not serve: the
// listing and the channel must agree, or the pages describe fiction.
func TestIndexRefusesNotesTheChannelDoesNotServe(t *testing.T) {
	source, _, keys := servedReleases(t, "2026.07.15-001")
	keys = append(keys, "2026.07.15-001/notes.md")

	if err := Index(source, keys, t.TempDir(), nil); err == nil {
		t.Error("Index accepted notes the channel does not serve")
	}
}

func TestIndexLinksTheGitHubRelease(t *testing.T) {
	dir, _ := indexed(t, "2026.07.15-001")

	page := pageAt(t, dir, "2026.07.15-001", "index.html")
	if !strings.Contains(page, "https://github.com/liken-sh/liken/releases/tag/v2026.07.15-001") {
		t.Errorf("the page does not link the GitHub release:\n%s", page)
	}
}
