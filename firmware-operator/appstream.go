package main

// An LVFS catalog is an AppStream XML file, compressed with zstd. It
// has one component for each firmware the service offers, with the
// GUIDs of the devices that the firmware flashes and the list of its
// releases:
//
//	<component type="firmware">
//	  <id>work.frame.Laptop.RyzenAI300.BIOS.firmware</id>
//	  <provides><firmware type="flashed">9c13b7f1-…</firmware></provides>
//	  <releases><release version="0.0.4.2">…</release></releases>
//	</component>
//
// fwupd in the scan pod chooses the release that applies to a device.
// The operator reads a catalog file only to find the machines whose
// offered releases changed when the file changed, so for each GUID it
// keeps the component IDs and versions and nothing else. The reader
// streams the XML, so the whole document is never in memory.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// catalogIndex maps each GUID of one catalog file to the sorted
// releases of the components that flash it, each as
// "<component id> <version>". The index is only a cache. After a
// restart the operator downloads the file again, by the name and
// SHA-256 in the FirmwareCatalog's status.
type catalogIndex struct {
	file     string
	releases map[string][]string
}

// maxCatalogSize limits the decompressed size of a catalog, so that a
// decompression bomb cannot exhaust the operator's memory. readCatalog
// stops reading at this size, and the truncated XML then fails to parse.
const maxCatalogSize = 512 << 20

func readCatalog(file string, compressed io.Reader) (catalogIndex, error) {
	decoder, err := zstd.NewReader(compressed, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return catalogIndex{}, fmt.Errorf("decompressing the catalog: %w", err)
	}
	defer decoder.Close()
	releases, err := parseAppStream(io.LimitReader(decoder, maxCatalogSize))
	if err != nil {
		return catalogIndex{}, err
	}
	return catalogIndex{file: file, releases: releases}, nil
}

// firmwareComponent collects the index's fields of one component while
// the parser reads it.
type firmwareComponent struct {
	id       string
	guids    []string
	versions []string
}

func parseAppStream(document io.Reader) (map[string][]string, error) {
	decoder := xml.NewDecoder(document)
	releases := map[string][]string{}
	var path []string
	var current *firmwareComponent
	var text *string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing the catalog XML: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			parent := ""
			if len(path) > 0 {
				parent = path[len(path)-1]
			}
			path = append(path, t.Name.Local)
			switch {
			case t.Name.Local == "component" && attribute(t, "type") == "firmware":
				current = &firmwareComponent{}
			case current == nil:
			case t.Name.Local == "id" && parent == "component":
				text = &current.id
			case t.Name.Local == "firmware" && parent == "provides" && attribute(t, "type") == "flashed":
				current.guids = append(current.guids, "")
				text = &current.guids[len(current.guids)-1]
			case t.Name.Local == "release" && parent == "releases":
				current.versions = append(current.versions, attribute(t, "version"))
			}
		case xml.CharData:
			if text != nil {
				*text += string(t)
			}
		case xml.EndElement:
			path = path[:len(path)-1]
			text = nil
			if t.Name.Local == "component" && current != nil && !slices.Contains(path, "component") {
				current.index(releases)
				current = nil
			}
		}
	}
	for guid := range releases {
		slices.Sort(releases[guid])
		releases[guid] = slices.Compact(releases[guid])
	}
	return releases, nil
}

// index adds a component's releases under each GUID that it flashes.
// fwupd reports GUIDs in lowercase, so the keys are lowercase.
func (c *firmwareComponent) index(releases map[string][]string) {
	id := strings.TrimSpace(c.id)
	for _, guid := range c.guids {
		guid = strings.ToLower(strings.TrimSpace(guid))
		if guid == "" {
			continue
		}
		for _, version := range c.versions {
			releases[guid] = append(releases[guid], id+" "+version)
		}
	}
}

func attribute(element xml.StartElement, name string) string {
	for _, a := range element.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// releasesHash returns a hash of the releases that the file offers for
// a machine's GUIDs. When a new file gives the same hash as the file of
// the machine's last scan, the new file offers that machine nothing new.
func (ix catalogIndex) releasesHash(guids []string) string {
	sum := sha256.New()
	for _, guid := range sortedSet(guids) {
		fmt.Fprintf(sum, "%s\x00%s\x00", guid, strings.Join(ix.releases[guid], "\x01"))
	}
	return shortHex(sum.Sum(nil))
}

// sortedSet returns the distinct values, in lowercase and sorted.
func sortedSet(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.ToLower(v))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// shortHex returns the first 16 hex digits of a hash, short enough for
// a person to compare in a status.
func shortHex(sum []byte) string {
	return hex.EncodeToString(sum)[:16]
}
