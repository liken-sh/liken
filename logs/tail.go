package main

// The file relays implement a hand-written version of tail -F:
// follow a growing file, and when the file at the path suddenly
// becomes a different file, finish the old one and start the new
// one. This rename sequence is exactly what init's rotate-at-boot
// produces (along with the in-boot size cap on k3s.log). This is
// also why the DaemonSets mount the parent directory instead of the
// file: a bind mount of the file itself would keep the old inode
// pinned forever, while a lookup through the directory sees each new
// generation.
//
// Rotation is detected by identity, not by name. The tailer holds
// the inode it opened, and when the path's (device, inode) pair no
// longer matches, the held file is the renamed previous generation.
// The tailer drains that file to its final EOF, because the writer
// may have added a few last lines before the rename, and then
// reopens the path from the top. Rotated generations that sit on
// disk (.1, .2, ...) are never read. They exist for boot forensics,
// and shipping previous boots is left unsolved here on purpose.
//
// The follow mechanism is polling. inotify could push updates
// instead, but a half-second poll is only two wakeups a second,
// gives sub-second shipping latency, needs no event plumbing, and
// behaves the same way on every filesystem. This kind of plain,
// predictable behavior belongs in code that runs under PID 1.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
	"time"
)

var pollInterval = 500 * time.Millisecond

// tailCursor is the resume point: which file, identified by identity
// rather than name, and the offset of the first byte of the next
// line. Offsets always point at the start of a line. Because of
// this, a resumed relay re-reads a partially sent line as a whole
// line, instead of sending a fragment.
type tailCursor struct {
	Dev    uint64 `json:"dev"`
	Ino    uint64 `json:"ino"`
	Offset int64  `json:"offset"`
}

// fileIdentity reads the (device, inode) pair that identifies a file
// independent of its path.
func fileIdentity(info fs.FileInfo) (uint64, uint64) {
	st := info.Sys().(*syscall.Stat_t)
	return uint64(st.Dev), st.Ino
}

// awaitFile opens the path, and waits for the file to exist.
// containerd.log is not created until k3s brings containerd up.
// After a rotation, there is also a moment before the writer creates
// the file again.
func awaitFile(ctx context.Context, path string) (*os.File, error) {
	for {
		f, err := os.Open(path)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// tailFile follows one log file forever, until the context ends. It
// sends an envelope for each line, using the line's starting byte
// offset as its sequence number. Each pass of the loop handles one
// generation of the file: open it, decide where to start reading,
// and follow it until a rotation replaces it.
func tailFile(ctx context.Context, path string, out *envelopeWriter, curDir string, now func() time.Time) error {
	var cur tailCursor
	resuming := loadCursor(curDir, &cur)

	for {
		f, err := awaitFile(ctx, path)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		dev, ino := fileIdentity(info)

		// Resume only into the same file that the cursor described,
		// and only within its current size. A shrunken file means
		// something truncated it in place, and nothing on a liken
		// machine should do that. Report this and start over, instead
		// of reading from the middle of unrelated bytes.
		offset := int64(0)
		if resuming && dev == cur.Dev && ino == cur.Ino {
			if cur.Offset <= info.Size() {
				offset = cur.Offset
				_ = out.notice(now(), "info", uint64(offset), nil,
					fmt.Sprintf("resuming %s at offset %d", path, offset))
			} else {
				_ = out.notice(now(), "warning", uint64(cur.Offset), nil,
					fmt.Sprintf("%s shrank below the cursor; replaying from the head", path))
			}
		}
		resuming = false

		if err := followGeneration(ctx, f, path, offset, dev, ino, out, curDir, now); err != nil {
			return err
		}
	}
}

// followGeneration reads one generation of the file. It seeks to the
// starting offset, follows the file as it grows, and when a rotation
// replaces the file at the path, drains the renamed generation to
// its final EOF. This function owns the open file, and closes it on
// every return. A nil return means the generation rotated, and the
// caller should reopen the path.
func followGeneration(ctx context.Context, f *os.File, path string, offset int64, dev, ino uint64, out *envelopeWriter, curDir string, now func() time.Time) error {
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}

	// This is the line assembler. partial carries an incomplete line
	// across reads. lineStart is the file offset of that line's
	// first byte, and also the seq of the next line sent. pos is the
	// offset of the next unread byte.
	var partial []byte
	lineStart, pos := offset, offset

	// ship sends one complete line as an envelope, using the line's
	// starting byte offset as its sequence number.
	ship := func(line string) error {
		when, severity := lift(line, now())
		return out.emit(envelope{
			Time:     when.UTC().Format(time.RFC3339Nano),
			Severity: severity,
			Seq:      uint64(lineStart),
			Message:  line,
		})
	}

	buf := make([]byte, 32*1024)
	consume := func(data []byte) error {
		for len(data) > 0 {
			nl := bytes.IndexByte(data, '\n')
			if nl < 0 {
				partial = append(partial, data...)
				pos += int64(len(data))
				return nil
			}
			line := string(append(partial, data[:nl]...))
			partial = partial[:0]
			pos += int64(nl + 1)
			if err := ship(line); err != nil {
				return err
			}
			lineStart = pos
			data = data[nl+1:]
		}
		return nil
	}

	var lastCheckpoint time.Time
	rotated := false
	for !rotated {
		n, err := f.Read(buf)
		if n > 0 {
			if err := consume(buf[:n]); err != nil {
				return err
			}
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			return err
		}

		// The tailer has caught up. Checkpoint (at a limited rate),
		// sleep for one poll interval, then check whether the path
		// still names the file this loop holds.
		if t := now(); t.Sub(lastCheckpoint) >= checkpointInterval {
			lastCheckpoint = t
			if err := saveCursor(curDir, tailCursor{Dev: dev, Ino: ino, Offset: lineStart}); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
		st, err := os.Stat(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			rotated = true
		} else if d, i := fileIdentity(st); d != dev || i != ino {
			rotated = true
		}
	}

	// The held file is the renamed previous generation. Drain
	// whatever the writer appended before the rename. Send a
	// trailing unterminated line as it is: a crash mid-write is the
	// only way such a line can exist, and it will never be
	// completed. Then move on to the new file at the path.
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if err := consume(buf[:n]); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	if len(partial) > 0 {
		return ship(string(partial))
	}
	return nil
}

// relayFile implements the k3s and containerd verbs.
func relayFile(path string) error {
	return tailFile(context.Background(), path, newEnvelopeWriter(os.Stdout), cursorDir, time.Now)
}
