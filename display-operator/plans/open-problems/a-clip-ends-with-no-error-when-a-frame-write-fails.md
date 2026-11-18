# A clip ends with no error when a frame write fails

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`feed` in `capturestream.go` writes each captured frame to ffmpeg's
stdin with `writeCropped`. For a still, `feed` returns the error of
`writeCropped`. In the loop of a clip or a stream, it returns `nil` on
the same error. So a crop that reads past the end of the frame, or a
failed write to ffmpeg's pipe, ends the clip as a success: `stream`
records no failure, `endTruncated` does not run, and the log gets no
line.

## Evidence

- `capturestream.go`, `feed`, the still path:
  `return writeCropped(writer, frame, stride, crop)`.
- The loop: `if err := writeCropped(...); err != nil { return nil }`.
- `writeCropped` returns "the frame holds %d bytes and the crop reads
  %d" for a crop past the frame, and the pipe's error for a failed
  write.

## What would confirm it

A test that runs `feed` for a clip with a first frame shorter than the
crop. The still path returns the crop error for the same input, and
the clip path should too.

## What a fix has to decide

A failed write to the pipe can mean that ffmpeg exited, and `stream`
already reports an ffmpeg exit through `encoder.wait()`. The fix
decides whether the loop returns every error, or returns the crop
error and leaves a closed pipe to the encoder's own exit status.
