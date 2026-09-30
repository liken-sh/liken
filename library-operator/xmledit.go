package main

// xmledit.go is the surgical edit of one element in an XML document: the
// .nfo edit every fact makes goes through it, so every byte another tool
// wrote outside that element stays as it was.

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
)

// The element an edit inserts or replaces, and the attribute that tells one
// uniqueid from another where a document holds several.
type xmlElement struct {
	name      string
	attribute string
	value     string
}

// The surgical edit: one element in, every other byte as it was, so nothing
// another tool wrote is lost. The whole document is never parsed into values
// and written back, because a round trip drops every element the parser does
// not model.
func editElement(document []byte, element xmlElement, replacement []byte) ([]byte, error) {
	spans, err := elementSpans(document, element)
	if err != nil {
		return nil, err
	}
	if spans.start >= 0 {
		return splice(document, spans.start, spans.end, replacement), nil
	}
	if spans.rootEnd < 0 {
		return nil, errors.New("the document has no root element to insert into")
	}
	return splice(document, spans.rootEnd, spans.rootEnd, spans.insertion(document, replacement)), nil
}

// hasRootElement reports whether a document holds an element to edit. An
// empty file, or an XML declaration with nothing under it, holds none. A
// document the parser stops on counts as holding one here, so the edit itself
// names the error and the bytes stay as they were.
func hasRootElement(document []byte) bool {
	decoder := lenientXML(document)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		if _, isStart := token.(xml.StartElement); isStart {
			return true
		}
	}
}

// An inserted element takes the indentation the document's own children
// carry, so the edit reads as the same hand wrote it. The indentation the
// replacement already carries is dropped first, so the block is indented once
// and not twice.
func (s documentSpans) insertion(document, replacement []byte) []byte {
	lead := trailingWhitespace(document[:s.rootEnd])
	block := append([]byte{}, replacement...)
	if len(afterLastNewline(lead)) == 0 && s.firstChild >= 0 {
		indent := afterLastNewline(trailingWhitespace(document[:s.firstChild]))
		block = append(append([]byte{}, indent...), bytes.TrimLeft(replacement, " \t")...)
	}
	return append(block, lead...)
}

// Only the run after the last newline counts as indentation. Blank lines
// above it belong to the document's spacing, not to the child's margin.
func afterLastNewline(space []byte) []byte {
	if at := bytes.LastIndexByte(space, '\n'); at >= 0 {
		return space[at+1:]
	}
	return space
}

// The result is a new slice, so the caller's document is never written over.
func splice(document []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(document)-(end-start)+len(replacement))
	out = append(out, document[:start]...)
	out = append(out, replacement...)
	return append(out, document[end:]...)
}

// The run of whitespace before the root's end tag is repeated after an
// inserted element, so the indentation the document already had holds.
func trailingWhitespace(document []byte) []byte {
	at := len(document)
	for at > 0 && isXMLSpace(document[at-1]) {
		at--
	}
	return document[at:]
}

func isXMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// The three places in a document an edit reads: the element it may replace,
// where the root's end tag begins, and where the first child begins.
type documentSpans struct {
	start      int
	end        int
	rootEnd    int
	firstChild int
}

// Reads those three places in one pass over the document, so the edit needs
// no parse of the whole tree into values. Only the root's direct children are
// candidates, because every element the facts edit sits there.
func elementSpans(document []byte, element xmlElement) (documentSpans, error) {
	spans := documentSpans{start: -1, end: -1, rootEnd: -1, firstChild: -1}
	decoder := lenientXML(document)
	depth := 0
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return spans, nil
		}
		if err != nil {
			return spans, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			depth++
			if depth != 2 {
				continue
			}
			if spans.firstChild < 0 {
				spans.firstChild = before
			}
			if spans.start >= 0 || !elementMatches(typed, element) {
				continue
			}
			if err := decoder.Skip(); err != nil {
				return spans, err
			}
			spans.start, spans.end, depth = before, int(decoder.InputOffset()), depth-1
		case xml.EndElement:
			depth--
			if depth == 0 && spans.rootEnd < 0 {
				spans.rootEnd = before
			}
		}
	}
}

// An element with no attribute named matches by its name alone, which is the
// ordinary case.
func elementMatches(token xml.StartElement, element xmlElement) bool {
	if token.Name.Local != element.name {
		return false
	}
	if element.attribute == "" {
		return true
	}
	for _, attribute := range token.Attr {
		if attribute.Name.Local == element.attribute && attribute.Value == element.value {
			return true
		}
	}
	return false
}
