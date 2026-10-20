package main

// These tests read jcats and catalog files the way LVFS serves them,
// and the ones that a broken mirror or a mistake could serve.

import (
	"slices"
	"strings"
	"testing"
)

// A jcat answers its first item's file and SHA-256, whatever follows
// the gzip stream.
func TestReadJcat(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	cases := []struct {
		name    string
		body    []byte
		want    jcatEntry
		wantErr string
	}{
		{"LVFS's jcat", jcatOf("firmware-09138-stable.xml.zst", sum), jcatEntry{"firmware-09138-stable.xml.zst", sum}, ""},
		{"an uppercase checksum", jcatOf("f.xml.zst", strings.ToUpper(sum)), jcatEntry{"f.xml.zst", sum}, ""},
		{"not gzip", []byte("{}"), jcatEntry{}, "not gzip"},
		{"a cut stream", jcatOf("f.xml.zst", sum)[:20], jcatEntry{}, "does not decompress"},
		{"not JSON", gzipOf("<xml/>", nil), jcatEntry{}, "not JSON"},
		{"no item", gzipOf(`{"Items":[]}`, nil), jcatEntry{}, "names no file"},
		{"a path", jcatOf("../secrets", sum), jcatEntry{}, "not a file name"},
		{"no SHA-256", gzipOf(`{"Items":[{"Id":"f.xml.zst","Blobs":[{"Kind":4,"Data":"aa"}]}]}`, nil), jcatEntry{}, "no SHA-256"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readJcat(c.body)
			if got != c.want || (err == nil) != (c.wantErr == "") || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("readJcat = %+v, %v; want %+v and an error with %q", got, err, c.want, c.wantErr)
			}
		})
	}
}

// The index keeps, for each GUID a firmware component flashes, the
// component's releases, under the GUID in lowercase, the way fwupd
// reports it. A GUID a component only requires, and a component of
// another type, add nothing.
func TestReadCatalogIndexesFlashedGUIDs(t *testing.T) {
	document := appStreamOf(frameworkBIOS("0.0.3.5", "0.0.4.2"), dbx("2"),
		fwComponent{id: "com.vendor.ssd", guids: []string{"ABCDEF00-0000-0000-0000-000000000003"}, versions: []string{"7"}}) +
		`<component type="desktop-application"><provides><firmware type="flashed">` + meGUID + `</firmware></provides></component>`
	index, err := readCatalog("f.xml.zst", strings.NewReader(string(zstdOf(document))))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		guid string
		want []string
	}{
		{biosGUID, []string{frameworkBIOS().id + " 0.0.3.5", frameworkBIOS().id + " 0.0.4.2"}},
		{"abcdef00-0000-0000-0000-000000000003", []string{"com.vendor.ssd 7"}},
		{dbxGUID, []string{"com.microsoft.dbx.x64.firmware 2"}},
		{meGUID, nil},
		{"00000000-0000-0000-0000-00000000ffff", nil},
	}
	for _, c := range cases {
		t.Run(c.guid, func(t *testing.T) {
			if got := index.releases[c.guid]; !slices.Equal(got, c.want) {
				t.Errorf("the releases of %s are %v, want %v", c.guid, got, c.want)
			}
		})
	}
}

// A file that is not a zstd stream of XML does not read.
func TestReadCatalogRefusesWhatIsNotACatalog(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"not zstd", "plain"},
		{"not XML", string(zstdOf("<components><component"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := readCatalog("f", strings.NewReader(c.body)); err == nil {
				t.Error("readCatalog read it, want an error")
			}
		})
	}
}

// The hash of a machine's releases changes with a release of one of its
// GUIDs, and not with a release of another GUID or the order of its
// GUIDs.
func TestTheReleasesHash(t *testing.T) {
	first := catalogIndex{releases: map[string][]string{biosGUID: {"bios 1"}, dbxGUID: {"dbx 1"}}}
	cases := []struct {
		name  string
		index catalogIndex
		guids []string
		same  bool
	}{
		{"the same catalog in another order", first, []string{dbxGUID, biosGUID}, true},
		{"a new BIOS release", catalogIndex{releases: map[string][]string{biosGUID: {"bios 1", "bios 2"}, dbxGUID: {"dbx 1"}}}, []string{biosGUID, dbxGUID}, false},
		{"a release of another GUID", catalogIndex{releases: map[string][]string{biosGUID: {"bios 1"}, dbxGUID: {"dbx 1"}, meGUID: {"me 1"}}}, []string{biosGUID, dbxGUID}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.index.releasesHash(c.guids) == first.releasesHash([]string{biosGUID, dbxGUID})
			if got != c.same {
				t.Errorf("the hashes are equal: %v, want %v", got, c.same)
			}
		})
	}
}

// The versioned file is in the directory of the catalog's URL.
func TestFileURL(t *testing.T) {
	got, err := fileURL(lvfsURL, "firmware-09138-stable.xml.zst")
	if want := "https://cdn.fwupd.org/downloads/firmware-09138-stable.xml.zst"; got != want || err != nil {
		t.Errorf("fileURL = %q, %v; want %q", got, err, want)
	}
	if _, err := fileURL("%zz", "f"); err == nil {
		t.Error("fileURL took a URL that does not parse")
	}
}
