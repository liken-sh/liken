package main

// PROSE: this file is the one door every enricher write to a library volume
// goes through, and the rules below are what keep a bad write from losing a
// file a person cares about.

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PROSE: the mark every temporary carries, which the remove below checks for
// and the scanner reads as a junk name.
const likenTempMark = ".liken-tmp-"

// PROSE: the modes a new file and a new directory take on the volume.
const (
	volumeFilePerm      fs.FileMode = 0o644
	volumeDirectoryPerm fs.FileMode = 0o755
)

// PROSE: one Job's writes to the volume, named by the Job so two Jobs never
// share a temporary.
type volumeWriter struct {
	job string
}

// PROSE: says why an unnamed Job still gets a usable temporary name.
func newVolumeWriter(job string) *volumeWriter {
	if job == "" {
		job = "job"
	}
	return &volumeWriter{job: job}
}

// PROSE: says why the temporary sits in the target's own directory and carries
// the target's name.
func (w *volumeWriter) temporary(target string) string {
	dir, base := filepath.Split(target)
	return filepath.Join(dir, base+likenTempMark+w.job)
}

// PROSE: the whole write rule in one function: a temporary in the same
// directory, flushed, then renamed onto the target, so a crash leaves a stray
// temporary and never a half-written file.
func (w *volumeWriter) write(target string, data []byte) error {
	temporary := w.temporary(target)
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, volumeFilePerm)
	if err != nil {
		return err
	}
	if err := writeAndSync(file, data); err != nil {
		file.Close()
		_ = w.removeTemporary(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = w.removeTemporary(temporary)
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = w.removeTemporary(temporary)
		return err
	}
	return nil
}

// PROSE: says why the bytes reach the disk before the rename names them.
func writeAndSync(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

// PROSE: the one remove in the enrichers, and why it refuses every name that
// does not carry the temporary mark.
func (w *volumeWriter) removeTemporary(path string) error {
	if !strings.Contains(filepath.Base(path), likenTempMark) {
		return fmt.Errorf("refusing to remove %s: it carries no %s mark", path, likenTempMark)
	}
	return os.Remove(path)
}

// PROSE: says why a directory is created before a file lands in it.
func (w *volumeWriter) writeInto(directory, name string, data []byte) error {
	if err := os.MkdirAll(directory, volumeDirectoryPerm); err != nil {
		return err
	}
	return w.write(filepath.Join(directory, name), data)
}

// PROSE: names the element an edit inserts or replaces, and the attribute that
// tells one uniqueid from another where a document holds several.
type xmlElement struct {
	name      string
	attribute string
	value     string
}

// PROSE: the surgical edit: one element in, every other byte as it was, so
// nothing another tool wrote is lost.
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

// PROSE: says why an inserted element takes the indentation the document's own
// children carry, so the edit reads as the same hand wrote it.
func (s documentSpans) insertion(document, replacement []byte) []byte {
	lead := trailingWhitespace(document[:s.rootEnd])
	block := append([]byte{}, replacement...)
	if len(afterLastNewline(lead)) == 0 && s.firstChild >= 0 {
		indent := afterLastNewline(trailingWhitespace(document[:s.firstChild]))
		block = append(append([]byte{}, indent...), block...)
	}
	return append(block, lead...)
}

// PROSE: says why only the run after the last newline counts as indentation.
func afterLastNewline(space []byte) []byte {
	if at := bytes.LastIndexByte(space, '\n'); at >= 0 {
		return space[at+1:]
	}
	return space
}

// PROSE: says that the replacement is copied into a new slice so the caller's
// document is never written over.
func splice(document []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(document)-(end-start)+len(replacement))
	out = append(out, document[:start]...)
	out = append(out, replacement...)
	return append(out, document[end:]...)
}

// PROSE: says why the run of whitespace before the root's end tag is repeated
// after an inserted element, so the indentation the document already had holds.
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

// PROSE: the three places in a document an edit reads: the element it may
// replace, where the root's end tag begins, and where the first child begins.
type documentSpans struct {
	start      int
	end        int
	rootEnd    int
	firstChild int
}

// PROSE: reads those three places in one pass over the document, so the edit
// needs no parse of the whole tree into values.
func elementSpans(document []byte, element xmlElement) (documentSpans, error) {
	spans := documentSpans{start: -1, end: -1, rootEnd: -1, firstChild: -1}
	decoder := xml.NewDecoder(bytes.NewReader(document))
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

// PROSE: says that an element with no attribute named matches by its name
// alone, which is the ordinary case.
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
