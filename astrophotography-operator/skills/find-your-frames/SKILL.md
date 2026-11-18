---
name: find-your-frames
description: "Find a target's frames in its archive claim, choose their file names with spec.archive.format and its tokens such as {object}, {night}, {filter}, and {datetime}, read the night date and the FRAMEID suffix in a name, query the frame catalog with jq, copy the frames to a computer with a short-lived pod and kubectl exec tar, and check each file against the SHA-256 the catalog recorded. Use when a person wants their frames, asks where a frame went or why it has that name, or wants to check the archive."
---

This skill is the guide at https://liken.sh/astrophotography/docs/guides/find-your-frames/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Find and verify your frames

At the end of this guide, a target's frames are on your computer, and
you have checked each file against the SHA-256 that the catalog
recorded when the frame reached the archive.

You need:

* A `Target` with frames in its archive. `kubectl get target` counts
  them in the `PROGRESS` column, and `status.sequences` counts them as
  `archived`.
* `kubectl` access to the target's namespace. This guide uses the
  target `m42` in `observatory`.
* `tar`, `jq`, and `sha256sum` on your computer.

## Where the frames are

Each frame is a file in the target's archive, the claim in
`spec.archive.claimName`, under the directory `spec.archive.path`:

```sh
kubectl get target m42 -n observatory -o jsonpath='{.spec.archive}'
```

```
{"claimName":"archive","format":"{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}","path":"astro","promotion":"Continuous"}
```

With the default format, the archive looks like this:

```
astro/
  catalog/
    m42.jsonl
  M42/
    2026-10-13/
      Luminance/
        M42_Luminance_300s_20261014T021506Z_4f2a9c01d3e7.fits
        M42_Luminance_300s_20261014T022049Z_8be1d3075a92.fits
      Red/
        M42_Red_300s_20261014T034412Z_c0a17e2f9b44.fits
```

A frame is in the archive only after a promotion `Job` has copied it
and checked the copy. Until then it is in the reservation's outbox, at
`/outbox/<Camera>/<Reservation>/<Target>/<name>.fits`, and the target
counts it as `pending`.

## Choose the file names

`spec.archive.format` is the template for each frame's file name,
relative to `archive.path` and without the extension. Each `/` starts
a directory. The operator fills in the tokens before each exposure,
the camera writes the frame under that name, and promotion never
renames it. When you change the format, only the frames taken after
the change get the new names.

| Token | Value |
|---|---|
| `{target}` | the `Target`'s name |
| `{object}` | `spec.object` |
| `{filter}` | the sequence's filter, or `none` |
| `{exposure}` | the exposure in seconds, such as `300s` or `0.5s` |
| `{binning}` | such as `1x1`, or `default` when the sequence leaves the camera's binning |
| `{gain}`, `{offset}` | the sequence's values, or `default` |
| `{camera}`, `{telescope}`, `{reservation}` | the names of those resources |
| `{train}` | the camera's `OpticalTrain`, or `none` |
| `{night}` | the date of the night, as `YYYY-MM-DD` |
| `{datetime}` | the time the operator requested the exposure, in UTC, such as `20261014T021506Z` |
| `{n}` | the frame's number in its sequence, with four digits |
| `{frameid}` | the frame's `FRAMEID`, a UUIDv7 |

Outside the tokens, the format can contain letters, digits, `.`, `_`,
`+`, `-`, and `/`. In a token's value, every other character becomes
`_`, so an object named `M 42` gives `M_42`.

`{night}` gives every frame of a night the same date, including the
frames after midnight. It is the date 12 hours before the exposure, in
local mean solar time at the longitude in the `Observatory`'s
`spec.location.longitude`. So every frame from one local noon to the
next has the same date, and the frames of the night of 13 October 2026
all go into `2026-10-13/`.

## Why a name ends with 12 digits

The camera's driver overwrites a file of the same name without an
error. So unless the format contains `{frameid}`, the operator appends
`_` and the last 12 hexadecimal digits of the frame's `FRAMEID`, as in
`M42_Luminance_300s_20261014T021506Z_4f2a9c01d3e7`. Those digits are
random, and among 1,000 frames the chance that two share them is about
2 in a billion. `{n}` does not make a name unique, because a restart
of the operator can count one frame twice.

The whole `FRAMEID` is in the frame's header and in its catalog line
as `frameID`, so you can find the line of a file that someone renamed.

## Read the catalog

Each target has one catalog, a JSON Lines file at
`<archive.path>/catalog/<Target>.jsonl` in the archive. A promotion
`Job` appends one line for each frame after it checks the frame's copy
in the archive, and never changes a line:

```json
{"record":"frame","frameID":"0199e1c2-5b40-7000-8000-4f2a9c01d3e7","target":"m42","reservation":"east-tonight","telescope":"east","camera":"east-main","train":"east-imaging","path":"M42/2026-10-13/Luminance/M42_Luminance_300s_20261014T021506Z_4f2a9c01d3e7.fits","sizeBytes":2108160,"sha256":"9f86d0...","startTime":"2026-10-14T02:15:06.864Z","filter":"Luminance","exposureSeconds":300,"binning":1,"gain":100,"offset":10}
```

`path` is relative to `archive.path`, and `sha256` is the hash of the
whole file. [The catalog](https://liken.sh/astrophotography/docs/concepts/archive/#the-catalog) lists
every field. The target's counts come from the catalog, so deleting a
frame's file from the archive does not lower `archived`.

## Copy the frames to your computer

Start a pod that mounts the archive claim read-only and runs `sleep`,
so you can read from it with `kubectl exec`:

```sh
kubectl run -n observatory frames-out --image=busybox:1.38.0 --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"out","image":"busybox:1.38.0","command":["sleep","3600"],
    "volumeMounts":[{"name":"archive","mountPath":"/archive","readOnly":true}]}],
    "volumes":[{"name":"archive","persistentVolumeClaim":{"claimName":"archive","readOnly":true}}]}}'
kubectl wait -n observatory --for=condition=Ready pod/frames-out
```

If the claim is a `local-path` volume, the scheduler starts the pod
on the node that has it. Copy the whole archive path, with the
catalog, through `tar`:

```sh
mkdir -p ~/frames
kubectl exec -n observatory frames-out -- tar -C /archive -cf - astro \
  | tar -C ~/frames -xf -
```

To copy one night, name its directory and the catalog, such as
`astro/M42/2026-10-13 astro/catalog` in place of `astro`. When you are
done, delete the pod:

```sh
kubectl delete -n observatory pod frames-out
```

## Check the frames

`jq` turns the catalog into the input of `sha256sum --check`. Run it
from the archive path, because each `path` is relative to it:

```sh
cd ~/frames/astro
jq -r 'select(.record == "frame") | "\(.sha256)  \(.path)"' catalog/m42.jsonl \
  | sha256sum --check --quiet
```

The command prints nothing when every file matches. It prints a line
for each file that differs or is missing, and exits with a status
other than `0`. When you copied one night, check only that night's
lines:

```sh
jq -r 'select(.record == "frame" and (.path | startswith("M42/2026-10-13/"))) | "\(.sha256)  \(.path)"' \
  catalog/m42.jsonl | sha256sum --check --quiet
```

You can query the catalog for other facts about the frames. This
lists the frames of one reservation:

```sh
jq -r 'select(.reservation == "east-tonight") | .path' catalog/m42.jsonl
```

This prints the total exposure in hours for each filter:

```sh
jq -s 'map(select(.record == "frame")) | group_by(.filter)
  | map({filter: .[0].filter, hours: (map(.exposureSeconds) | add / 3600)})' catalog/m42.jsonl
```

When a frame's count or file is not what you expect, see
[Troubleshoot](https://liken.sh/astrophotography/docs/guides/troubleshoot/#a-frame-does-not-reach-the-archive).
