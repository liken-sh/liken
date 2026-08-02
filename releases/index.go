package releases

// This file renders the channel's index pages: a front page listing
// every published release, a page for each release, and a page for
// the source mirror.
//
// Object storage cannot build these pages. The bucket's own ACL is
// private while each object is public-read, so a named path downloads
// anonymously and the root refuses to say what exists. The hostname
// class the channel answers on serves an index document for any
// prefix, and generates no listing of its own. So the pages are
// objects like any other, and something has to write them.
//
// Nothing here is new information. A release document already names
// every artifact with its digest and size, and records the version of
// each component inside. The version list is the set of top-level
// prefixes. Each page is therefore a render of a document the channel
// already serves, which makes the whole tree derived: no digest names
// a page, no machine reads one, and deleting them all would cost the
// channel nothing but its readability. That is what makes a rerun
// safe, and it is why one command both backfills the releases that
// were published before these pages existed and keeps up with each
// new one.
//
// Listing the bucket needs a credential and rendering does not, so
// the two are split at the key list: the caller supplies the keys,
// and this code fetches only public documents. The S3 protocol
// therefore stays out of the toolkit that ships to operators, and a
// test drives the renderer from a list it writes by hand.

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"html/template"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/brand"
	"github.com/liken-sh/liken/machine"
)

//go:embed page.html.tmpl index.html.tmpl release.html.tmpl sources.html.tmpl
var pageTemplates embed.FS

// Index renders a channel's pages into outDir. keys are the channel's
// object keys, one per line as its storage reports them, and source
// is the base URL the channel answers on. The pages link from the
// channel's root, so outDir's contents belong at the root of whatever
// serves them.
func Index(source string, keys []string, outDir string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	source = strings.TrimSuffix(source, "/")

	versions, notes, sources := readKeys(keys)
	if len(versions) == 0 {
		return fmt.Errorf("the key list names no release")
	}

	// The channel document names which release is marked latest,
	// rather than the highest version in the list. The document is what
	// a polling cluster reads, so a page that disagreed with it would
	// be telling an operator something no machine acts on.
	latest, err := resolveLatest(source)
	if err != nil {
		return err
	}

	channel := &channelView{page: newPage("liken releases"), Source: source, Sources: sources}
	for _, version := range versions {
		release, err := releaseView(source, version, notes[version])
		if err != nil {
			return err
		}
		release.IsLatest = version == latest
		release.HasSources = len(sources) > 0
		channel.Releases = append(channel.Releases, release)
	}

	pages, err := template.ParseFS(pageTemplates, "*.html.tmpl")
	if err != nil {
		return err
	}
	if err := writePage(pages, "index.html.tmpl", channel, outDir, "index.html"); err != nil {
		return err
	}
	for _, release := range channel.Releases {
		if err := writePage(pages, "release.html.tmpl", release, outDir, release.Version, "index.html"); err != nil {
			return err
		}
	}
	// A channel with no mirror gets no sources page, because an empty
	// page under a license obligation reads as an offer with nothing
	// behind it.
	if len(sources) > 0 {
		mirror := &channelView{page: newPage("liken sources"), Source: source, Sources: sources}
		if err := writePage(pages, "sources.html.tmpl", mirror, outDir, "sources", "index.html"); err != nil {
			return err
		}
	}

	// The versions document is the front page's machine-readable twin,
	// written from the same releases the pages were rendered from
	// (versions.go).
	document := versionsDocument(latest, channel.Releases)
	if err := os.WriteFile(filepath.Join(outDir, "versions.yaml"), document, 0o644); err != nil {
		return err
	}

	fmt.Fprintf(out, "%d releases indexed, latest %s\n", len(channel.Releases), latest)
	return nil
}

// page is what every template needs, whichever page it renders. The
// stylesheet and the mark are inlined rather than linked: the channel
// answers when the cluster that serves liken.sh does not, so a page
// here fetches nothing (brand/liken.css says more).
type page struct {
	Title      string
	Stylesheet template.CSS
	Mark       template.HTML
}

func newPage(title string) page {
	return page{
		Title:      title,
		Stylesheet: template.CSS(brand.Stylesheet),
		Mark:       template.HTML(brand.Mark),
	}
}

// channelView is the front page's and the sources page's input.
type channelView struct {
	page
	Source   string
	Releases []*releaseInfo
	Sources  []*sourceComponent
}

// releaseInfo is one release's page, and one row of the front page.
type releaseInfo struct {
	page
	Version    string
	Digest     string
	Artifacts  []artifactInfo
	Components []machine.ReleaseComponent
	IsLatest   bool
	HasSources bool
	// Notes is the release's changes list, from the channel's own
	// notes.md beside the release. The notes are announcement prose:
	// no digest pins them and no machine reads them, which is what
	// lets a release published before notes existed gain them later.
	Notes string
}

// Component gives one component's version for the front page's
// columns, and an empty string when the release carries no component
// by that name. An older release predates a component that a newer
// one records, and a blank cell is the honest way to show that.
func (r *releaseInfo) Component(name string) string {
	for _, component := range r.Components {
		if component.Name == name {
			return component.Version
		}
	}
	return ""
}

type artifactInfo struct {
	Name   string
	SHA256 string
	Human  string
}

type sourceComponent struct {
	Name     string
	Versions []*sourceVersion
}

type sourceVersion struct {
	Version string
	Files   []sourceFile
}

type sourceFile struct {
	Name string // the file's own name, for the link's text
	Path string // <component>/<version>/<name>, under /sources/
}

// readKeys sorts a channel's object keys into the releases it holds,
// the releases that carry notes, and the source mirror. Anything else
// on the channel, the channel document and the pages themselves
// included, belongs to no listing and is skipped. A key whose first
// segment does not parse as a version is not a release, which is what
// keeps a stray prefix from becoming a page.
func readKeys(keys []string) ([]string, map[string]bool, []*sourceComponent) {
	versions := map[string]bool{}
	notes := map[string]bool{}
	components := map[string]map[string][]sourceFile{}
	for _, key := range keys {
		segments := strings.Split(strings.TrimPrefix(key, "/"), "/")
		switch {
		case len(segments) == 2 && api.ValidVersion(segments[0]) == nil:
			versions[segments[0]] = true
			if segments[1] == "notes.md" {
				notes[segments[0]] = true
			}
		case len(segments) == 4 && segments[0] == "sources":
			component, version, name := segments[1], segments[2], segments[3]
			if components[component] == nil {
				components[component] = map[string][]sourceFile{}
			}
			components[component][version] = append(components[component][version],
				sourceFile{Name: name, Path: component + "/" + version + "/" + name})
		}
	}

	// Newest first: the release an operator wants is nearly always one
	// of the last few, and a rollback needs the one below the top.
	ordered := slices.Collect(maps.Keys(versions))
	slices.SortFunc(ordered, func(a, b string) int { return api.CompareVersions(b, a) })

	// Everything else sorts by name, so that the same channel renders
	// the same bytes on every run.
	var sources []*sourceComponent
	for _, name := range slices.Sorted(maps.Keys(components)) {
		component := &sourceComponent{Name: name}
		for _, version := range slices.Sorted(maps.Keys(components[name])) {
			files := components[name][version]
			slices.SortFunc(files, func(a, b sourceFile) int { return strings.Compare(a.Name, b.Name) })
			component.Versions = append(component.Versions, &sourceVersion{Version: version, Files: files})
		}
		sources = append(sources, component)
	}
	return ordered, notes, sources
}

// releaseView fetches one release's document from the channel and
// arranges it for the page. Fetching over the public URL is also a
// check: a release the channel does not serve gets no page, and the
// error names it. withNotes says the key list saw a notes object
// beside this release, so a fetch that fails then is the same
// disagreement between the listing and the channel, not an old
// release from before notes existed.
func releaseView(source, version string, withNotes bool) (*releaseInfo, error) {
	raw, err := fetchDocument(source + "/" + version + "/release.yaml")
	if err != nil {
		return nil, fmt.Errorf("fetching %s's release document: %w", version, err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return nil, fmt.Errorf("%s's release document does not parse: %w", version, err)
	}
	if release.Metadata.Name != version {
		return nil, fmt.Errorf("the document under %s names version %s", version, release.Metadata.Name)
	}

	info := &releaseInfo{
		page: newPage("liken " + version),
		// The digest is computed from the document's exact bytes, the
		// same value the release workflow prints and an operator pins
		// in a catalog entry. It is computed here and never read from
		// the channel, because a digest the channel supplied would
		// vouch for nothing.
		Version:    version,
		Digest:     fmt.Sprintf("sha256:%x", sha256.Sum256(raw)),
		Components: release.Components,
	}
	for _, artifact := range release.Artifacts {
		info.Artifacts = append(info.Artifacts, artifactInfo{
			Name:   artifact.Name,
			SHA256: artifact.SHA256,
			Human:  humanSize(artifact.Size),
		})
	}
	if withNotes {
		raw, err := fetchDocument(source + "/" + version + "/notes.md")
		if err != nil {
			return nil, fmt.Errorf("fetching %s's notes: %w", version, err)
		}
		info.Notes = strings.TrimSpace(string(raw))
	}
	return info, nil
}

// writePage renders one template to one path under the output
// directory.
func writePage(pages *template.Template, name string, data any, outDir string, parts ...string) error {
	path := filepath.Join(append([]string{outDir}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := pages.ExecuteTemplate(f, name, data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
