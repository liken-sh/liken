# 40, Test patterns

Built on 2026-09-30. A `Play` can name a test pattern, a white screen
or colour bars, with a `pattern://` URI. The player image carries the
pattern files, so a pattern plays on any `Player` with no storage, no
network, and no film. No drill has run on `liken-1` yet.

## The problem

The display draws its OSD over the film, and every check of the OSD
depends on the frame under it. The scrims banded for months, and the
banding showed only over a bright, flat frame. To find one, a person
had to search a film for a bright scene, start a `Play` there, and
pause it. The first search found a dark kitchen. A letterboxed film
also hides the top and the bottom of the screen behind black bars,
which are the rows the scrims cover.

A test pattern answers this. A white screen is the worst case for a
dark scrim, and colour bars show a colour or a range error at a glance.

## The design

### The URI

A pattern URI names the pattern and, optionally, the frame size:

    pattern://white
    pattern://bars/1920x1080
    pattern://white/1920x804

The patterns are `white` and `bars`, the SMPTE HD colour bars. The frames are the sizes of real screens and of a letterboxed
film:

| Frame | Shape | What it matches |
| --- | --- | --- |
| `1280x720` | 16:9 | a 720p screen |
| `1920x1080` | 16:9 | a 1080p screen |
| `3840x2160` | 16:9 | a 4K screen |
| `2560x1080` | 21:9 | a wide 1080-row screen |
| `3840x1600` | 2.4:1 | a wide 1600-row screen |
| `1920x804` | 2.39:1 | a scope film, which letterboxes on a 16:9 screen |

A URI with no frame takes the frame of the `Player`'s screen. The
operator reads the monitor from the `Player`'s status and the
compositor's mode from that `Display`, and chooses the largest frame of
the screen's shape that fits on it. Two shapes count as one when their
ratios differ by under 2 percent, because screens sold as 21:9 range from
2.37:1 to 2.4:1. A screen whose shape matches no
frame, or a `Player` whose screen is not known yet, takes `1920x1080`.
A URI that names a frame takes that frame on any screen, so a scope
frame letterboxes on a 16:9 screen on purpose.

An unknown pattern or frame fails the `Play` before any pod exists, the
way an unknown scheme does. The message lists what exists.

The operator resolves the frame when it builds the pod, and the pod
keeps its items for its whole run. A `Display` that changes mode later
does not replace the pod.

### The files

Each file is ten minutes long, at one frame each second, in H.264 in
Matroska. One frame a second keeps a still pattern small. mpv reports
the position and the duration from the timestamps, so the scrubber
advances as it does for a film. Each file carries a chapter every
minute, so the scrubber draws its chapter marks, and a title such as
"White, 1920×1080", so the header shows a name and not the file name.

The encode makes one minute and copies it ten times with no second
encode, so each minute starts on a keyframe and a seek decodes at most
one minute of frames. Each encode runs on one thread with no lookahead.
With x264's defaults, one 3840x2160 encode held 4.1 GB of memory, and a
first try that ran every encode at once ran a 30 GB workstation out of
memory. On one thread the encode holds 360 MB.

The 12 files measure 1.47 MB in total, from 39 KB for white at 1280x720
to 208 KB for the bars at 3840x2160. `make -j2 patterns` writes them in
4.5 seconds on a workstation.

### The build

The files are committed in `patterns/`, and the component's `Makefile`
makes them: each file is a target, and the recipe is the encode. The
player image copies them to `/usr/share/liken/patterns/`, so neither
the image build nor CI runs an encoder, and the image carries none.
Nothing checks that the committed files match the recipe. An edit to
the recipe runs `make -B -j2 patterns` and commits the files it writes.

`local/video` plays a pattern as it plays any file, by its path in
`patterns/`.

## Considered and set aside

* **A live lavfi source.** mpv plays `av://lavfi:color=...` with no
  file. mpv 0.41 did not know that source's duration: at 0:20 of a
  60-second pattern it reported 0:22. The scrubber and the time left
  are part of what the patterns check, so a live source fails the
  purpose.
* **Lavfi graphs in the URI.** A `Play` that passed a filter graph to
  mpv could name the `movie` source, which opens any path or URL, and
  make the playback pod read files. The patterns are a closed list.
* **A build stage that encodes the files.** A stage on the
  repository's `ffmpeg` image, with a static `busybox` for a shell,
  encoded all the files in 5.9 seconds, and bake caches it. It keeps
  the binaries out of the history. At 1.5 MB, committed files are
  simpler: the image build does less, and a workstation plays them
  with no step first. A committed file can drift from its recipe, and a
  check that they match needs byte-identical output from the encoder,
  which x264 does not promise across versions.
* **A black pattern.** It cost as much as white and adds little that
  the bars do not show.
* **HEVC.** One 3840x2160 file came out larger in HEVC, 307 KB against
  208 KB, and took four times the CPU.
* **Pick the frame in the pod.** The shim that starts mpv does not know
  the screen's size, and the operator already watches the `Display`s.

## How it will be proved

On `liken-1`, on the `Player` named `lab-portable`, a 1920x1080 panel.

* `pattern://white` plays with the frame `1920x1080`. A pause shows the
  OSD over white, and a screenshot through `display-api` shows the
  scrims fade with no bands.
* The scrubber shows ten minutes, a chapter mark each minute, and the
  title "White, 1920×1080".
* `pattern://white/1920x804` letterboxes.
* `pattern://purple` fails the `Play` with a message that lists the
  patterns.
