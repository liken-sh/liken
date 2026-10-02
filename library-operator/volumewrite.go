package main

// volumewrite.go is the one door every enricher write to a library volume
// goes through. On the lab that volume is the production copy, so the rules
// here are what keep a bad write from losing a file a person cares about: a
// temporary and a rename, a remove that refuses every name but a
// temporary's, a remove that takes one file this operator wrote and nothing
// else reads, the trickplay map, the move and the remove a merge of two
// .contributors/ entries makes, and the replace of a thumbnail or a tile
// directory made from the file a path held before. The edit of one element
// in an .nfo file is in xmledit.go, and the door for a file a writer reads,
// changes, and writes back is in volumeupdate.go.

import (
	"errors"
	"fmt"
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

// One container's writes to the volume, named by the Job and the container
// so two writers never share a temporary.
type volumeWriter struct {
	job string
	// The phases volume, where the .nfo locks are, and empty where the
	// writer is alone, as in a test.
	locks string
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

// land renames a temporary this writer filled onto its target, for a file
// too large to hold in memory, which write cannot take. It refuses a source
// with no temporary mark, so it moves only what this binary wrote.
func (w *volumeWriter) land(temporary, target string) error {
	if !strings.Contains(filepath.Base(temporary), likenTempMark) {
		return fmt.Errorf("refusing to move %s: it carries no %s mark", temporary, likenTempMark)
	}
	if filepath.Dir(temporary) != filepath.Dir(target) {
		return fmt.Errorf("refusing to move %s out of its own directory", temporary)
	}
	return os.Rename(temporary, target)
}

// removeDatasetCopy deletes one superseded copy of an IMDb dataset file from
// the provider's cache claim. The cache is not a library volume, and the
// remove refuses every name but the one the cache gives a copy: sixteen
// lowercase hex characters and the .tsv.gz suffix.
func (w *volumeWriter) removeDatasetCopy(path string) error {
	if !isDatasetCopyName(filepath.Base(path)) {
		return fmt.Errorf("refusing to remove %s: it is not a dataset copy", path)
	}
	return os.Remove(path)
}

// The WebVTT map earlier releases of the trickplay fact wrote beside the sheets.
const trickplayMapName = "tiles.vtt"

// Whether a path names that map, and only that map: the file name, inside a
// layout folder, inside a .trickplay directory. A person's subtitle named
// tiles.vtt sits beside a video and never two levels under a .trickplay.
func isTrickplayMap(path string) bool {
	if filepath.Base(path) != trickplayMapName {
		return false
	}
	return filepath.Ext(filepath.Dir(filepath.Dir(path))) == trickplayExtension
}

// The second remove in the enrichers. It takes the map alone and refuses every
// other path, so the guard above is the whole rule.
func (w *volumeWriter) removeTrickplayMap(path string) error {
	if !isTrickplayMap(path) {
		return fmt.Errorf("refusing to remove %s: it is no %s under a %s directory",
			path, trickplayMapName, trickplayExtension)
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

// The update door for a file in a directory that may not exist yet, as a
// title's .liken directory is before its first ledger.
func (w *volumeWriter) updateInto(directory, name string, change func([]byte) ([]byte, error)) ([]byte, error) {
	if err := os.MkdirAll(directory, volumeDirectoryPerm); err != nil {
		return nil, err
	}
	return w.update(filepath.Join(directory, name), change)
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

// Whether a directory is one entry of a .contributors/ store: the entry, under
// its two-character bucket, under the store.
func isContributorEntry(dir string) bool {
	return filepath.Base(filepath.Dir(filepath.Dir(dir))) == contributorsDirectory
}

// The move a merge makes of a person's biography or headshot, from the entry
// the merge removes to the entry that stays. The link fails where the entry
// that stays holds a file of that name, and that file is kept, so the move
// never replaces a file. The answer says whether this call moved the file. It
// refuses every other name and every directory outside the store.
func (w *volumeWriter) moveContributorFile(from, to string) (bool, error) {
	name := filepath.Base(from)
	if (name != contributorBiographyName && name != contributorHeadshotName) || filepath.Base(to) != name ||
		!isContributorEntry(filepath.Dir(from)) || !isContributorEntry(filepath.Dir(to)) {
		return false, fmt.Errorf("refusing to move %s to %s: it is no %s or %s of a %s entry",
			from, to, contributorBiographyName, contributorHeadshotName, contributorsDirectory)
	}
	if err := os.Link(from, to); errors.Is(err, fs.ErrExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, os.Remove(from)
}

// The remove of an entry a merge removed, once no credit names it. It takes
// the whole directory, and it refuses every directory that is not an entry of
// the store or whose contributor.yaml holds anything but mergedInto, so a
// person's entry is out of reach of this call.
func (w *volumeWriter) removeMergedEntry(dir string) error {
	held, data, err := readContributorFile(filepath.Join(dir, contributorFileName))
	if err != nil {
		return err
	}
	record := held.MergedInto != "" && held.Name == "" && len(held.IDs) == 0 &&
		held.Born == "" && held.Died == ""
	if !isContributorEntry(dir) || data == nil || !record {
		return fmt.Errorf("refusing to remove %s: it is no %s entry that holds only mergedInto",
			dir, contributorsDirectory)
	}
	return os.RemoveAll(dir)
}

// The suffix of the one file name replaceEarlierFile takes: an episode's
// thumbnail, which the art phase names for the episode file it goes beside.
const episodeThumbSuffix = "-thumb.jpg"

// The replace door for an episode's thumbnail that shows an earlier file at
// the episode's path. A thumbnail is a frame of one encode, and a new encode
// can place that frame at another time or crop it another way, so the still
// of the earlier file is not this file's still. The door writes the new
// bytes over the file only while the file on the volume is older than
// before, the second the new file took the path. A thumbnail at or after
// that second was made for this file by some writer, and the door keeps it.
// It refuses every name but a thumbnail's, so no other file a person kept is
// in its reach. The answer says whether this call wrote the file.
func (w *volumeWriter) replaceEarlierFile(target string, data []byte, before int64) (bool, error) {
	if !strings.HasSuffix(filepath.Base(target), episodeThumbSuffix) {
		return false, fmt.Errorf("refusing to replace %s: it is no %s", target, episodeThumbSuffix)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return w.createOnce(target, data)
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.ModTime().Unix() >= before {
		return false, nil
	}
	return true, w.write(target, data)
}

// The replace door for a tile directory that shows an earlier file, which
// the trickplay worker staged a new tree for. The tiles of an earlier encode
// place each thumbnail at that encode's times, so they are not this file's
// tiles. The door takes the directory only while it is older than before,
// the second the new file took the path. A directory at or after that
// second was made for this file, by Jellyfin or by an earlier run, and the
// door keeps it and clears the staging. It refuses every name but a
// trickplay directory's.
//
// The earlier tree moves aside under a temporary name, the staged tree takes
// the real name, and then the earlier tree goes. A failure between the two
// renames moves the earlier tree back, so a reader finds a whole directory
// at the real name at every step except the moment between the renames.
func (w *volumeWriter) replaceEarlierTree(target string, before int64) (bool, error) {
	if !strings.EqualFold(filepath.Ext(target), trickplayExtension) {
		return false, fmt.Errorf("refusing to replace %s: it is no %s directory", target, trickplayExtension)
	}
	staging := w.temporary(target)
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return w.createTree(target)
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.ModTime().Unix() >= before {
		return false, w.removeTemporaryTree(staging)
	}
	if err := syncTree(staging); err != nil {
		_ = w.removeTemporaryTree(staging)
		return false, err
	}
	earlier := w.temporary(target + ".earlier")
	if err := w.removeTemporaryTree(earlier); err != nil {
		return false, err
	}
	if err := os.Rename(target, earlier); err != nil {
		_ = w.removeTemporaryTree(staging)
		return false, err
	}
	if err := os.Rename(staging, target); err != nil {
		_ = os.Rename(earlier, target)
		_ = w.removeTemporaryTree(staging)
		return false, err
	}
	return true, w.removeTemporaryTree(earlier)
}
