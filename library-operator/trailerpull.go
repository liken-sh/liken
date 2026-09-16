package main

// trailerpull.go is the pull of one trailer onto the volume: the two
// temporaries, the remux, the check ffprobe makes of the result, and the
// write that never replaces a file that exists.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// How long one remux may run. It is a variable so that no test waits it out.
var trailerRemuxTimeout = 10 * time.Minute

// The bounds a pulled file's length must fall inside, so a whole film and a
// still frame both fail the check. The floor is under a TV spot's length,
// because a spot is a kind the trailer fact records, and a title whose best
// fetchable video is a spot gets the spot.
const (
	trailerShortest = 10 * time.Second
	trailerLongest  = 8 * time.Minute
)

// The names of the two temporaries of one pull.
const (
	trailerPullMark  = "pull"
	trailerRemuxMark = "remux"
)

// A temporary the walk skips, in the folder the file lands in, so the rename
// is one directory entry. The mark is what lets removeTemporary take it back.
func (w *volumeWriter) hiddenTemporary(directory, name string) string {
	return filepath.Join(directory, "."+name+likenTempMark+w.job)
}

// The write a pulled file lands through. It never replaces a file that
// exists, which is the rule the art fact's own write holds, and the bytes of
// the temporary are on the disk before the link points at them.
func (w *volumeWriter) createOnceFrom(temporary, target string) (bool, error) {
	if !strings.Contains(filepath.Base(temporary), likenTempMark) {
		return false, fmt.Errorf("refusing to land %s: it carries no %s mark",
			temporary, likenTempMark)
	}
	if err := os.MkdirAll(filepath.Dir(target), volumeDirectoryPerm); err != nil {
		return false, err
	}
	if err := syncPath(temporary); err != nil {
		return false, err
	}
	linked := linkFile(temporary, target)
	if errors.Is(linked, fs.ErrExist) {
		return false, nil
	}
	if linked == nil {
		return true, nil
	}
	return copyOnceFrom(temporary, target)
}

// The link call. It is a variable so that a test can drive the filesystems
// that refuse a link: a volume mounted from a share, and a target on another
// device.
var linkFile = os.Link

// The same rule where no link can be made. The create decides, because O_EXCL
// fails on a file that exists the way the link does, and the bytes are on the
// disk before the call returns.
func copyOnceFrom(temporary, target string) (bool, error) {
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, volumeFilePerm)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	from, err := os.Open(temporary)
	if err != nil {
		file.Close()
		return false, err
	}
	defer from.Close()
	if _, err := io.Copy(file, from); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return false, err
	}
	return true, file.Close()
}

// The one call that rewrites the container: no decode, the streams copied,
// and the index at the front so a player starts on the first read.
func ffmpegRemux(ctx context.Context, input, output string) error {
	timed, cancel := context.WithTimeout(ctx, trailerRemuxTimeout)
	defer cancel()

	command := exec.CommandContext(timed, "ffmpeg", "-nostdin", "-loglevel", "error",
		"-i", input, "-c", "copy", "-movflags", "+faststart", "-y", output)
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg %s: %w: %s", filepath.Base(input), err,
			strings.TrimSpace(string(out)))
	}
	return nil
}

// What the remuxed file must hold before it lands: a video stream, and a
// length inside the bounds above.
func trailerFileHolds(ctx context.Context, path string) error {
	output, err := probeFile(ctx, path)
	if err != nil {
		return err
	}
	var read ffprobeAnswer
	if err := json.Unmarshal(output, &read); err != nil {
		return fmt.Errorf("reading the probe of %s: %w", filepath.Base(path), err)
	}
	record := read.probedFile()
	videos := 0
	for _, stream := range record.Streams {
		if stream.Kind == fileTypeVideo {
			videos++
		}
	}
	if videos == 0 {
		return fmt.Errorf("%s holds no video stream", filepath.Base(path))
	}
	length := time.Duration(record.Duration * float64(time.Second))
	if length < trailerShortest || length > trailerLongest {
		return fmt.Errorf("%s runs %s, outside %s to %s", filepath.Base(path),
			length.Round(time.Second), trailerShortest, trailerLongest)
	}
	return nil
}

// The whole pull of one file: the stream, the remux, the check, and the
// write. Every path out removes both temporaries, and the caller records the
// attempt the result names. A file that already exists under the name ends
// the pull before one byte is taken, and the write's own check is the last
// defense.
func (e *enricher) pullTrailerFile(ctx context.Context, source trailerSource,
	item identityItem, row trailerRow, file trailerFile, folder string) (*trailerFileEntry, string) {
	// A trailers folder is made inside a title's own folder alone, because a
	// title at the library root has no folder to hold one, and a trailers
	// folder at the root belongs to no title.
	if relativePath(e.root, folder) == "." {
		e.logf("%s has no folder of its own, so this fact pulls no trailer for it", item.id)
		return nil, attemptNothing
	}
	directory := filepath.Join(folder, trailersFolderName)
	if err := os.MkdirAll(directory, volumeDirectoryPerm); err != nil {
		e.logf("could not make %s: %v", relativePath(e.root, directory), err)
		return nil, attemptError
	}
	target := filepath.Join(directory, safeTrailerName(row.Name)+trailerFileExtension)
	stands, err := fileExists(target)
	if err != nil {
		e.logf("could not read %s: %v", relativePath(e.root, target), err)
		return nil, attemptError
	}
	if stands {
		e.logf("a file already stands at %s", relativePath(e.root, target))
		return nil, attemptFound
	}
	pulled := e.writer.hiddenTemporary(directory, trailerPullMark)
	remuxed := e.writer.hiddenTemporary(directory, trailerRemuxMark) + trailerFileExtension
	defer e.dropTrailerTemporary(pulled)
	defer e.dropTrailerTemporary(remuxed)

	size, err := pullTrailerBytes(ctx, source, file, pulled)
	if err != nil {
		e.logf("could not pull %s: %v", file.URL, err)
		return nil, attemptError
	}
	e.tallies.add(tallyTrailerFetchBytes, float64(size), "site", row.Site)
	if err := ffmpegRemux(ctx, pulled, remuxed); err != nil {
		e.logf("could not remux the trailer of %s: %v", item.id, err)
		return nil, attemptError
	}
	if err := trailerFileHolds(ctx, remuxed); err != nil {
		e.logf("the trailer of %s is no trailer: %v", item.id, err)
		return nil, attemptError
	}

	landed, err := e.writer.createOnceFrom(remuxed, target)
	if err != nil {
		e.logf("could not write %s: %v", relativePath(e.root, target), err)
		return nil, attemptError
	}
	if !landed {
		e.logf("a file already stands at %s", relativePath(e.root, target))
		return nil, attemptFound
	}
	e.logf("wrote %s, %dp and %d bytes, from %s",
		relativePath(e.root, target), file.Height, size, row.Site)
	return &trailerFileEntry{
		Provider: row.Provider, Key: row.Key, URL: file.URL,
		File:   relativePath(folder, target),
		Height: file.Height, Size: size, At: time.Now().UTC(),
	}, attemptFound
}

// A temporary that is not there is no failure, because every path out of the
// pull removes both of them.
func (e *enricher) dropTrailerTemporary(path string) {
	if held, _ := fileExists(path); !held {
		return
	}
	if err := e.writer.removeTemporary(path); err != nil {
		e.logf("could not clear %s: %v", path, err)
	}
}
