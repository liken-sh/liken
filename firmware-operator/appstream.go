package main

// An LVFS catalog is an AppStream XML file, compressed with zstd. It
// holds one component for each firmware the service offers, with the
// GUIDs of the devices that the firmware flashes and the list of its
// releases:
//
//	<component type="firmware">
//	  <id>work.frame.Laptop.RyzenAI300.BIOS.firmware</id>
//	  <provides><firmware type="flashed">9c13b7f1-…</firmware></provides>
//	  <releases><release version="0.0.4.2">…</release></releases>
//	</component>
//
// The operator reads the catalog only to decide which machines to scan
// again when the catalog names a new file, so it keeps, for each GUID,
// the components and versions that a scan could offer for it. fwupd in
// the scan pod decides which release applies. LVFS's catalog is tens of
// megabytes of XML, so the reader streams it and keeps nothing else.

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

// catalogIndex is what the operator keeps of one catalog file: for
// each GUID, the releases of the components that flash it, as
// "<component id> <version>", sorted. The index is a cache. The
// operator reads the file again after a restart when it needs the
// index, and the Catalog's status holds the file's name and SHA-256.
type catalogIndex struct {
	file     string
	releases map[string][]string
}

// maxCatalogSize bounds the decompressed catalog. LVFS's catalog is
// tens of megabytes, so a stream past this size is a mistake or a
// decompression bomb, and the reader refuses it.
const maxCatalogSize = 512 << 20

// readCatalog decompresses and parses a catalog file.
func readCatalog(file string, compressed io.Reader) (catalogIndex, error) {
	decoder, err := zstd.NewReader(compressed, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return catalogIndex{}, fmt.Errorf("the catalog is not zstd: %w", err)
	}
	defer decoder.Close()
	releases, err := parseAppStream(io.LimitReader(decoder, maxCatalogSize))
	if err != nil {
		return catalogIndex{}, err
	}
	return catalogIndex{file: file, releases: releases}, nil
}

// firmwareComponent is the part of one firmware component that the
// index keeps, while the parser reads it.
type firmwareComponent struct {
	id       string
	guids    []string
	versions []string
}

// parseAppStream reads each firmware component of an AppStream
// document, and answers the releases of each GUID.
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
			return nil, fmt.Errorf("the catalog is not XML: %w", err)
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

// index adds a component's releases under each GUID it flashes. fwupd
// reports a GUID in lowercase, so the index keys it that way.
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

// releasesHash answers a hash of the catalog's releases for a set of
// GUIDs. Two catalog files give the same hash when they offer the same
// releases for those GUIDs, so a new file with the same hash has
// nothing new for the machine that reported them.
func (ix catalogIndex) releasesHash(guids []string) string {
	sum := sha256.New()
	for _, guid := range sortedSet(guids) {
		fmt.Fprintf(sum, "%s\x00%s\x00", guid, strings.Join(ix.releases[guid], "\x01"))
	}
	return shortHex(sum.Sum(nil))
}

// sortedSet answers the distinct values in order, in lowercase.
func sortedSet(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.ToLower(v))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// shortHex is the first 16 hex digits of a hash, enough to tell two
// sets apart in a status that a person reads.
func shortHex(sum []byte) string {
	return hex.EncodeToString(sum)[:16]
}
