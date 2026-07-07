package main

// The file relays are a hand-written tail -F: follow a growing file,
// and when the file at the path is suddenly a different file, finish
// the old one and start the new one. That rename dance is exactly
// what init's rotate-at-boot (and the in-boot size cap on k3s.log)
// produces, and the reason the DaemonSets mount the parent directory
// rather than the file: a bind mount of the file itself would pin
// the old inode forever, while a lookup through the directory sees
// each new generation.
//
// Rotation is detected by identity, not by name: the tailer holds
// the inode it opened, and when the path's (device, inode) no longer
// match, the held file is the renamed previous generation. It gets
// drained to its final EOF (the writer may have gotten a few last
// lines in before the rename) and the path is reopened from the top.
// Rotated generations sitting on disk (.1, .2, ...) are never read;
// they are boot forensics, and shipping previous boots is a problem
// this milestone deliberately left open.
//
// The follow mechanism is polling. inotify could push instead, but a
// half-second poll is two wakeups a second for sub-second shipping
// latency, needs no event plumbing, and behaves identically on every
// filesystem, which is the kind of boring that belongs under PID 1's
// logs.

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

// tailCursor is the resume point: which file (by identity, not
// name) and the offset of the first byte of the next line. Offsets
// only ever point at line starts, so a resumed relay re-reads a
// partially-shipped line whole rather than emitting a fragment.
type tailCursor struct {
	Dev    uint64 `json:"dev"`
	Ino    uint64 `json:"ino"`
	Offset int64  `json:"offset"`
}

// fileIdentity reads the (device, inode) pair that names a file
// independent of its path.
func fileIdentity(info fs.FileInfo) (uint64, uint64) {
	st := info.Sys().(*syscall.Stat_t)
	return uint64(st.Dev), st.Ino
}

// awaitFile opens the path, waiting for it to exist: containerd.log
// isn't created until k3s brings containerd up, and after a rotation
// there is a moment before the writer recreates the file.
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

// tailFile follows one log file forever (until the context ends),
// emitting an envelope per line with the line's starting byte offset
// as its sequence number.
func tailFile(ctx context.Context, path string, out *envelopeWriter, curDir string, now func() time.Time) error {
	var cur tailCursor
	resuming := loadCursor(curDir, &cur)
	var lastCheckpoint time.Time

	for {
		f, err := awaitFile(ctx, path)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			return err
		}
		dev, ino := fileIdentity(info)

		// Resume only into the same file the cursor described, and
		// only within its current size. A shrunken file means
		// something truncated it in place, which nothing on a liken
		// machine should do; say so and start over rather than read
		// from the middle of unrelated bytes.
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
		if offset > 0 {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return err
			}
		}

		// The line assembler: partial carries an incomplete line
		// across reads, lineStart is the file offset of its first
		// byte (and the seq of the next line emitted), pos is the
		// offset of the next unread byte.
		var partial []byte
		lineStart, pos := offset, offset
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
				when, severity := lift(line, now())
				if err := out.emit(envelope{
					Time:     when.UTC().Format(time.RFC3339Nano),
					Severity: severity,
					Seq:      uint64(lineStart),
					Message:  line,
				}); err != nil {
					return err
				}
				lineStart = pos
				data = data[nl+1:]
			}
			return nil
		}

		rotated := false
		for !rotated {
			n, err := f.Read(buf)
			if n > 0 {
				if err := consume(buf[:n]); err != nil {
					f.Close()
					return err
				}
			}
			if err == nil {
				continue
			}
			if !errors.Is(err, io.EOF) {
				f.Close()
				return err
			}

			// Caught up. Checkpoint (throttled), nap, then ask
			// whether the path still names the file we hold.
			if t := now(); t.Sub(lastCheckpoint) >= checkpointInterval {
				lastCheckpoint = t
				if err := saveCursor(curDir, tailCursor{Dev: dev, Ino: ino, Offset: lineStart}); err != nil {
					f.Close()
					return err
				}
			}
			select {
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			case <-time.After(pollInterval):
			}
			st, err := os.Stat(path)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					f.Close()
					return err
				}
				rotated = true
			} else if d, i := fileIdentity(st); d != dev || i != ino {
				rotated = true
			}
		}

		// The held file is the renamed previous generation. Drain
		// whatever the writer appended before the rename, ship a
		// trailing unterminated line as-is (a crash mid-write is the
		// only way one exists, and it will never be finished), and
		// move on to the new file at the path.
		for {
			n, err := f.Read(buf)
			if n > 0 {
				if err := consume(buf[:n]); err != nil {
					f.Close()
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				f.Close()
				return err
			}
		}
		if len(partial) > 0 {
			line := string(partial)
			when, severity := lift(line, now())
			if err := out.emit(envelope{
				Time:     when.UTC().Format(time.RFC3339Nano),
				Severity: severity,
				Seq:      uint64(lineStart),
				Message:  line,
			}); err != nil {
				f.Close()
				return err
			}
		}
		f.Close()
	}
}

// relayFile is the k3s and containerd verbs.
func relayFile(path string) error {
	return tailFile(context.Background(), path, newEnvelopeWriter(os.Stdout), cursorDir, time.Now)
}
