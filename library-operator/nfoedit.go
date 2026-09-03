package main

// The one-element edit of volumewrite.go, widened to the group of elements
// one fact owns. A group is the unit because a fact such as overview owns six
// elements and writes them together, and a rating sits under the ratings
// element beside the ratings other facts write. Every other byte of the
// document stays as it was.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
)

// The elements one fact owns, and the element they sit under where they are
// not the root's own children.
type elementGroup struct {
	parent string
	owned  []xmlElement
}

// Where one owned element starts and ends in the document.
type elementSpan struct {
	start int
	end   int
}

// What one pass over the document reads: every owned element, where the
// parent holds its children, and the two places an insert can land.
type groupPlaces struct {
	spans           []elementSpan
	parentEnd       int
	parentFirst     int
	parentIndentEnd int
	rootEnd         int
	firstChild      int
}

// One pass over the document that reads all of those, the way elementSpans
// does for one element, so a group edit needs no parse of the whole tree into
// values. The owned elements sit at one depth: the root's children, or the
// children of the named parent.
func groupSpans(document []byte, group elementGroup) (groupPlaces, error) {
	places := groupPlaces{parentEnd: -1, parentFirst: -1, parentIndentEnd: -1, rootEnd: -1, firstChild: -1}
	decoder := xml.NewDecoder(bytes.NewReader(document))
	depth, target := 0, 2
	inParent, parentRead := false, false
	if group.parent != "" {
		target = 3
	}
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return places, nil
		}
		if err != nil {
			return places, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				if places.firstChild < 0 {
					places.firstChild = before
				}
				if group.parent != "" && !parentRead && typed.Name.Local == group.parent {
					inParent, parentRead = true, true
					places.parentIndentEnd = before
				}
			}
			if depth == 3 && inParent && places.parentFirst < 0 {
				places.parentFirst = before
			}
			if depth != target || (group.parent != "" && !inParent) || !ownedElement(typed, group.owned) {
				continue
			}
			if err := decoder.Skip(); err != nil {
				return places, err
			}
			places.spans = append(places.spans, elementSpan{start: before, end: int(decoder.InputOffset())})
			depth--
		case xml.EndElement:
			depth--
			if depth == 1 && inParent {
				inParent = false
				places.parentEnd = before
			}
			if depth == 0 && places.rootEnd < 0 {
				places.rootEnd = before
			}
		}
	}
}

func ownedElement(token xml.StartElement, owned []xmlElement) bool {
	for _, element := range owned {
		if elementMatches(token, element) {
			return true
		}
	}
	return false
}

// The group edit writes the elements where the first owned element stood,
// takes the rest of them out, and leaves every other byte. Where the document
// holds none of them, the block goes in before the root's end tag, or under
// the parent element, which is created where the document has none.
func editElementGroup(document []byte, group elementGroup, elements [][]byte) ([]byte, error) {
	places, err := groupSpans(document, group)
	if err != nil {
		return nil, err
	}
	if len(places.spans) > 0 {
		return replaceGroup(document, places, elements), nil
	}
	if group.parent != "" && places.parentEnd >= 0 {
		return insertUnderParent(document, places, elements), nil
	}
	if places.rootEnd < 0 {
		return nil, errors.New("the document has no root element to insert into")
	}
	block := elementBlock(elements, childIndent(document, places.firstChild))
	if group.parent != "" {
		block = wrapInParent(group.parent, block, childIndent(document, places.firstChild))
	}
	spans := documentSpans{start: -1, end: -1, rootEnd: places.rootEnd, firstChild: places.firstChild}
	return splice(document, places.rootEnd, places.rootEnd, spans.insertion(document, block)), nil
}

// The block lands where the first owned element stood, so the group keeps the
// place a person or another writer gave it. The other owned elements go out
// with the whitespace that led them, so the edit leaves no blank line.
func replaceGroup(document []byte, places groupPlaces, elements [][]byte) []byte {
	first := places.spans[0]
	indent := afterLastNewline(trailingWhitespace(document[:first.start]))
	out := document
	for at := len(places.spans) - 1; at > 0; at-- {
		span := places.spans[at]
		lead := len(trailingWhitespace(document[:span.start]))
		out = splice(out, span.start-lead, span.end, nil)
	}
	return splice(out, first.start, first.end, elementBlock(elements, indent))
}

// A group whose parent exists but holds none of its elements goes in before
// the parent's end tag, at the indentation the parent's own children carry.
func insertUnderParent(document []byte, places groupPlaces, elements [][]byte) []byte {
	parentIndent := childIndent(document, places.parentIndentEnd)
	indent := append(append([]byte{}, parentIndent...), ' ', ' ')
	if places.parentFirst >= 0 {
		indent = childIndent(document, places.parentFirst)
	}
	lead := trailingWhitespace(document[:places.parentEnd])
	block := append(append([]byte{}, elementBlock(elements, indent)...), lead...)
	if len(lead) == 0 {
		block = append(append([]byte{'\n'}, indent...), elementBlock(elements, indent)...)
		block = append(append(block, '\n'), parentIndent...)
	}
	return splice(document, places.parentEnd, places.parentEnd, block)
}

// The indentation of an element is the run after the last newline before it,
// which is what an inserted element takes.
func childIndent(document []byte, at int) []byte {
	if at < 0 {
		return []byte("  ")
	}
	return append([]byte{}, afterLastNewline(trailingWhitespace(document[:at]))...)
}

// Every line of every element takes the group's own indentation, so an
// element with children reads as if the same hand wrote it.
func elementBlock(elements [][]byte, indent []byte) []byte {
	separator := append([]byte{'\n'}, indent...)
	lines := make([][]byte, len(elements))
	for at, element := range elements {
		lines[at] = bytes.ReplaceAll(element, []byte("\n"), separator)
	}
	return bytes.Join(lines, separator)
}

// A group under a parent the document does not hold arrives with that parent,
// so the first rating a fact writes creates the ratings element.
func wrapInParent(parent string, block, indent []byte) []byte {
	inner := append(append([]byte{}, indent...), ' ', ' ')
	nested := bytes.ReplaceAll(block, []byte("\n"+string(indent)), append([]byte{'\n'}, inner...))
	out := append([]byte("<"+parent+">\n"), inner...)
	out = append(out, nested...)
	out = append(out, '\n')
	out = append(out, indent...)
	return append(out, []byte("</"+parent+">")...)
}

// What the ledger records as wrote: a hash of the group's own bytes as the
// document holds them, so the next run tells the group it wrote from a group
// another writer changed. A document with none of the group's elements hashes
// to nothing.
func groupHash(document []byte, group elementGroup) (string, error) {
	places, err := groupSpans(document, group)
	if err != nil {
		return "", err
	}
	if len(places.spans) == 0 {
		return "", nil
	}
	held := make([][]byte, len(places.spans))
	for at, span := range places.spans {
		held[at] = document[span.start:span.end]
	}
	sum := sha256.Sum256(bytes.Join(held, []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}
