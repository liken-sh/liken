package main

// volumewrite.go is the one door every enricher write to a library volume
// goes through. On the lab that volume is the production copy, so the rules
// here are what keep a bad write from losing a file a person cares about: a
// temporary and a rename, an edit of one element, and a remove that refuses
// every name but a temporary's.

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

// The mark every temporary carries. The remove below checks for it, and the
// scanner reads it as a junk name, so a stray temporary never becomes a row.
const likenTempMark = ".liken-tmp-"

// The modes a new file and a new directory take on the volume.
const (
	volumeFilePerm      fs.FileMode = 0o644
	volumeDirectoryPerm fs.FileMode = 0o755
)

// One Job's writes to the volume, named by the Job so two Jobs never share a
// temporary.
type volumeWriter struct {
	job string
}

// A test or a local run may hold no Job name. The temporary still needs a
// suffix after the mark, so an unnamed Job writes as a Job called job.
func newVolumeWriter(job string) *volumeWriter {
	if job == "" {
		job = "job"
	}
	return &volumeWriter{job: job}
}

// The temporary sits in the target's own directory and carries the target's
// name, so the rename is one directory entry and a person who finds a stray
// knows which file it was for.
func (w *volumeWriter) temporary(target string) string {
	dir, base := filepath.Split(target)
	return filepath.Join(dir, base+likenTempMark+w.job)
}

// The whole write rule: a temporary in the same directory, flushed, then
// renamed onto the target. A crash leaves a stray temporary and never a
// half-written file. The rename lands on a target that may exist, which is
// how an edited .nfo replaces the one before it.
func (w *volumeWriter) write(target string, data []byte) error {
	temporary := w.temporary(target)
	if err := w.stage(temporary, data); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = w.removeTemporary(temporary)
		return err
	}
	return nil
}

// The write that never lands on a file that exists. The link fails where the
// target is there, and the filesystem itself decides, so two writers never
// lose one of the two files. The answer says whether this call wrote the
// file. Art takes this door and not write, because a poster another tool
// wrote is a file a person kept, and plan 30 leaves it.
func (w *volumeWriter) createOnce(target string, data []byte) (bool, error) {
	temporary := w.temporary(target)
	if err := w.stage(temporary, data); err != nil {
		return false, err
	}
	linked := os.Link(temporary, target)
	_ = w.removeTemporary(temporary)
	if errors.Is(linked, fs.ErrExist) {
		return false, nil
	}
	if linked != nil {
		return false, linked
	}
	return true, nil
}

// The temporary both writes start from: opened, written, flushed, and closed,
// so the bytes are on the disk before any name points at them. A failure
// takes the temporary with it.
func (w *volumeWriter) stage(temporary string, data []byte) error {
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
	return nil
}

// The bytes reach the disk before the rename names them, so a power loss
// after the rename never leaves an empty target on the volume.
func writeAndSync(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

// The one remove in the enrichers. It refuses every name that does not carry
// the temporary mark, so no code path in this binary can delete a file a
// person or another tool wrote. A test reads every other file for a remove
// and fails the build on one.
func (w *volumeWriter) removeTemporary(path string) error {
	if !strings.Contains(filepath.Base(path), likenTempMark) {
		return fmt.Errorf("refusing to remove %s: it carries no %s mark", path, likenTempMark)
	}
	return os.Remove(path)
}

// The staging door for a tool that writes its own files. The directory carries
// the temporary mark, so the remove below takes it and the scanner reads
// nothing under it as a title's file. A staging a crashed run left behind goes
// first, so the tool never reads that run's output as its own.
func (w *volumeWriter) stageTree(target string) (string, error) {
	staging := w.temporary(target)
	if err := w.removeTemporaryTree(staging); err != nil {
		return "", err
	}
	if err := os.MkdirAll(staging, volumeDirectoryPerm); err != nil {
		return "", err
	}
	return staging, nil
}

// The create door for a whole directory. Every file in the staged tree and
// every directory of it reaches the disk, then one rename lands the tree under
// its real name, so a reader sees the whole directory or none of it. The Lstat
// is what refuses a target that is there: it decides, because a rename onto an
// existing empty directory would succeed and take it. The rename's own failure
// on a directory that holds files is the backstop for the window between the
// two, where another writer created the target. A failure takes the staged
// tree with it, and the answer says whether this call landed it.
func (w *volumeWriter) createTree(target string) (bool, error) {
	staging := w.temporary(target)
	if _, err := os.Lstat(target); err == nil {
		return false, w.removeTemporaryTree(staging)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := syncTree(staging); err != nil {
		_ = w.removeTemporaryTree(staging)
		return false, err
	}
	if err := os.Rename(staging, target); err != nil {
		_ = w.removeTemporaryTree(staging)
		return false, err
	}
	return true, nil
}

// Every file first, then the directory that names it, so the rename above
// lands a tree whose bytes and whose entries are both on the disk.
func syncTree(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		under := filepath.Join(directory, entry.Name())
		if entry.IsDir() {
			err = syncTree(under)
		} else {
			err = syncPath(under)
		}
		if err != nil {
			return err
		}
	}
	return syncPath(directory)
}

// One open and one sync. A directory answers this call the way a file does,
// which is how the entries under it reach the disk.
func syncPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

// The remove that takes a staging directory and everything under it. It
// refuses every name that does not carry the temporary mark, the rule
// removeTemporary holds, so the files under a name a person wrote are out of
// reach of this binary.
func (w *volumeWriter) removeTemporaryTree(path string) error {
	if !strings.Contains(filepath.Base(path), likenTempMark) {
		return fmt.Errorf("refusing to remove %s: it carries no %s mark", path, likenTempMark)
	}
	return os.RemoveAll(path)
}

// A .liken directory does not exist until the first fact writes into it,
// so the directory is created before the file lands in it.
func (w *volumeWriter) writeInto(directory, name string, data []byte) error {
	if err := os.MkdirAll(directory, volumeDirectoryPerm); err != nil {
		return err
	}
	return w.write(filepath.Join(directory, name), data)
}

// The create door for a directory that may not exist yet, which is what a
// person's own directory under .contributors/ is on its first write. It is
// createOnce and never write, so a file another writer put there is kept.
func (w *volumeWriter) createInto(directory, name string, data []byte) (bool, error) {
	if err := os.MkdirAll(directory, volumeDirectoryPerm); err != nil {
		return false, err
	}
	return w.createOnce(filepath.Join(directory, name), data)
}

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
	decoder := xml.NewDecoder(bytes.NewReader(document))
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
