---
title: Target
weight: 100
toc: true
---

<!-- Generated from deploy/targets-crd.yaml by crdref. Do not edit. -->

# `Target`

A `Target` states an object to image and the frames wanted of it, as sequences of a filter, an exposure, and a count. `astrophotography-operator` images it in each `Reservation` of the same namespace whose `spec.holder` is `astrophotography-operator`, and moves each frame to the claim that `spec.archive` names. The target lasts until a person deletes it, and `status.sequences` keeps the progress across nights.

## spec

The object, its coordinates, the frames wanted, and where the frames go.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--object"></span>`object` | string | yes | The name of the object, such as `M42`. The operator writes it as `OBJECT` in each frame's header, and the token `{object}` of `spec.archive.format` gives it in each file name. It follows the rules of a declared keyword's value: printable ASCII and no `/`. |
| <span id="spec--coordinates"></span>`coordinates` | [object](#speccoordinates) | yes | Where the object is, in J2000 (ICRS), as every catalog gives it. SIMBAD and KStars print this form. The operator converts the coordinates to the equinox of the date before it slews, because an INDI mount takes `EQUATORIAL_EOD_COORD` at that equinox. |
| <span id="spec--priority"></span>`priority` | integer | no | Which eligible target a reservation images first: the highest priority, then the oldest target, then the first name. Optional. The default is `0`. |
| <span id="spec--suspend"></span>`suspend` | boolean | no | `true` keeps the target from being imaged and keeps its progress, the way `spec.suspend` of a `Job` does. Optional. The default is `false`. |
| <span id="spec--opticaltrain"></span>`opticalTrain` | string | no | The `OpticalTrain` to image through. Optional. A target that names a train is imaged only on that train's telescope. A target with no train is imaged on any telescope whose session has exactly one imaging camera. |
| <span id="spec--sequences"></span>`sequences` | [\[\]object](#specsequences) | yes | The frames wanted. The operator exposes the first sequence that has work left until it has none, and then the next, because a change of filter costs a refocus. A frame counts toward a sequence by its header: the same filter, exposure, binning, gain, and offset. So an edit of this list keeps the frames that still match a sequence. |
| <span id="spec--dither"></span>`dither` | [object](#specdither) | no | How PHD2 moves its lock position between frames, so that fixed noise of the sensor does not stack. Optional. With no `dither`, the operator does not dither. |
| <span id="spec--centering"></span>`centering` | [object](#speccentering) | no | How the operator centers the target after a slew: it takes a frame, solves it with the `Solver`, syncs the mount to the solved position, and slews again until the error is within `tolerance`. Optional. With no `centering`, the operator slews and does not solve. |
| <span id="spec--archive"></span>`archive` | [object](#specarchive) | yes | Where the frames go: a claim, a directory in it, and the file name of each frame. The camera writes each frame into the reservation's outbox under its final name, and the operator then moves it here, with the catalog of every frame at `<path>/catalog/<Target>.jsonl`. |
| <span id="spec--fitskeywords"></span>`fitsKeywords` | [\[\]object](#specfitskeywords) | no | The FITS keywords of this target's frames, such as `PROGRAM`. Optional. The operator writes them after the keywords that the session records for the imaging camera, so a keyword here replaces the value and the comment of the same name from the `Observatory`, the `Telescope`, the `Camera`, or the `Reservation`. The API server refuses a keyword that the camera's driver writes from what it measures, such as `EXPTIME`, and `OBJECT` and `FRAMEID`, which this operator writes. |

### spec.coordinates

Where the object is, in J2000 (ICRS), as every catalog gives it. SIMBAD and KStars print this form. The operator converts the coordinates to the equinox of the date before it slews, because an INDI mount takes `EQUATORIAL_EOD_COORD` at that equinox.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speccoordinates--ra"></span>`ra` | string | yes | The right ascension in hours, as `HH:MM:SS.s`, such as `05:35:17.3`. |
| <span id="speccoordinates--dec"></span>`dec` | string | yes | The declination in degrees, as `±DD:MM:SS.s`, such as `-05:23:28`. A declination north of the equator can leave out the `+`. |

### spec.sequences[]

One kind of frame and how many of it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsequences--filter"></span>`filter` | string | no | A name in the filter wheel's `FILTER_NAME`, such as `Luminance`, which the driver also writes as `FILTER`. Optional. With no filter, the operator leaves the light path as it is. |
| <span id="specsequences--exposure"></span>`exposure` | string | yes | The length of each frame, as a duration such as `300s` or `500ms`. |
| <span id="specsequences--count"></span>`count` | integer | yes | The number of frames wanted. |
| <span id="specsequences--binning"></span>`binning` | integer | no | The binning on both axes, written to `CCD_BINNING`. Optional. With none, the camera keeps its own. |
| <span id="specsequences--gain"></span>`gain` | integer | no | The gain, written to `CCD_GAIN` in the camera's own units. Optional. With none, the camera keeps its own. |
| <span id="specsequences--offset"></span>`offset` | integer | no | The offset, written to `CCD_OFFSET` in the camera's own units. Optional. With none, the camera keeps its own. |

### spec.dither

How PHD2 moves its lock position between frames, so that fixed noise of the sensor does not stack. Optional. With no `dither`, the operator does not dither.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdither--every"></span>`every` | integer | yes | The number of frames between two dithers. |
| <span id="specdither--pixels"></span>`pixels` | number | yes | The largest move of the lock position, in pixels of the guide camera. |
| <span id="specdither--raonly"></span>`raOnly` | boolean | no | `true` moves the lock position in right ascension alone, for a mount that guides in right ascension alone. Optional. The default is `false`. |

### spec.centering

How the operator centers the target after a slew: it takes a frame, solves it with the `Solver`, syncs the mount to the solved position, and slews again until the error is within `tolerance`. Optional. With no `centering`, the operator slews and does not solve.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speccentering--solver"></span>`solver` | string | yes | The `Solver` in the target's namespace. |
| <span id="speccentering--tolerance"></span>`tolerance` | number | no | The largest error that counts as centered, in arcseconds: the angle between the solved center and `spec.coordinates`. The default is `30`. Default: `30`. |
| <span id="speccentering--exposure"></span>`exposure` | string | no | The length of a centering frame, as a duration. When a frame does not solve, the operator tries once more with twice the exposure. The default is `5s`. Default: `5s`. |
| <span id="speccentering--filter"></span>`filter` | string | no | The filter of a centering frame. Optional. The default is the first sequence's filter. |
| <span id="speccentering--attempts"></span>`attempts` | integer | no | The number of rounds of solving and slewing before the operator gives up with the reason `CenteringFailed`. The default is `5`. Default: `5`. |

### spec.archive

Where the frames go: a claim, a directory in it, and the file name of each frame. The camera writes each frame into the reservation's outbox under its final name, and the operator then moves it here, with the catalog of every frame at `<path>/catalog/<Target>.jsonl`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specarchive--claimname"></span>`claimName` | string | yes | A `PersistentVolumeClaim` in the target's namespace. It cannot change after the target is created, because the counts come from the catalog in this claim. To use another claim, create another `Target`. A claim that is the outbox itself works: the operator then moves each frame with a rename. |
| <span id="specarchive--path"></span>`path` | string | no | A directory in the claim, such as `astro`. The frames and the catalog are under it. The default is the claim's root. Default: ``. |
| <span id="specarchive--format"></span>`format` | string | no | The file name of each frame, relative to `path` and with no extension, because the driver adds `.fits`. The tokens are `{target}`, `{object}`, `{filter}`, `{exposure}`, `{binning}`, `{gain}`, `{offset}`, `{camera}`, `{telescope}`, `{train}`, `{reservation}`, `{night}`, `{datetime}`, `{n}`, and `{frameid}`. Unless the format holds `{frameid}`, the operator adds `_` and the last 12 digits of the frame's `FRAMEID` to make each name unique. The rest of the text is the letters, the digits, `.`, `_`, `+`, `-`, and `/`. The default is `{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}`. Default: `{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}`. |
| <span id="specarchive--promotion"></span>`promotion` | string | no | When frames move to the archive. `Continuous` moves them while the night runs, so the outbox stays small. `AfterReservation` moves them when the reservation that wrote them ends, so the link stays quiet while the telescope works. The default is `Continuous`. One of: `Continuous`, `AfterReservation`. Default: `Continuous`. |

### spec.fitsKeywords[]

One keyword: its name, its value, and a comment.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specfitskeywords--name"></span>`name` | string | yes | The keyword, such as `OBSERVER`: 1 to 8 of the capital letters `A` to `Z`, the digits, `-`, and `_`. `INSTRUME` and `TELESCOP` are allowed, because the driver fills them with the names of INDI devices, such as `CCD Simulator`. |
| <span id="specfitskeywords--value"></span>`value` | string | yes | The value, at most 68 characters, the longest text that one 80-column card holds. The driver writes a value that reads as a number, such as `2026` or `-4.5`, as a number, and any other value as quoted text. A value cannot hold `/`, because the driver writes each `/` in a value as `\`. |
| <span id="specfitskeywords--comment"></span>`comment` | string | no | The comment that follows the value on the card. Optional. At most 47 characters, which fit beside a number. |

## status

What `astrophotography-operator` reports. A person never writes it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the status reflects. |
| <span id="status--progress"></span>`progress` | string | no | The frames in the archive of the frames wanted, over every sequence, such as `37/180`. |
| <span id="status--reservation"></span>`reservation` | string | no | The `Reservation` that images the target now. Empty while none does. A runner claims the target by writing it, so two telescopes never image one target at once. |
| <span id="status--sequences"></span>`sequences` | [\[\]object](#statussequences) | no | Each sequence of the spec, in the same order, with its counts. The work left of a sequence is `count - archived - pending`, so a target can end with more frames than it asked for when a promotion lags, and never with fewer. |
| <span id="status--outboxes"></span>`outboxes` | [\[\]object](#statusoutboxes) | no | The outbox claims that hold frames of this target that are not yet in the archive, so a frame that waits is never forgotten after its reservation ends. The operator keeps a finalizer on each claim listed here. |
| <span id="status--exposure"></span>`exposure` | [object](#statusexposure) | no | The frame in flight. The operator writes it before the exposure starts and clears it after the exposure ends, so an operator that restarts during an exposure counts the frame once. |
| <span id="status--centering"></span>`centering` | [object](#statuscentering) | no | The last result of centering. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Complete` is `True` when every sequence has at least its count of frames archived. `Imaging` is `True` while a reservation images the target, with the reason `Slewing`, `Centering`, `Guiding`, or `Exposing`, and a message that names the reservation and the sequence. `ArchiveAvailable` is `True` when the last promotion succeeded, and `False` with a reason such as `ClaimMissing` or `NameConflict` when it did not. |

### status.sequences[]

One sequence and its counts.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussequences--filter"></span>`filter` | string | no | The sequence's filter. |
| <span id="statussequences--exposure"></span>`exposure` | string | yes | The sequence's exposure. |
| <span id="statussequences--count"></span>`count` | integer | yes | The number of frames wanted. |
| <span id="statussequences--binning"></span>`binning` | integer | no | The sequence's binning. |
| <span id="statussequences--gain"></span>`gain` | integer | no | The sequence's gain. |
| <span id="statussequences--offset"></span>`offset` | integer | no | The sequence's offset. |
| <span id="statussequences--archived"></span>`archived` | integer | yes | The frames of this sequence in the archive's catalog, from the last promotion's report. |
| <span id="statussequences--pending"></span>`pending` | integer | yes | The frames of this sequence in an outbox and not yet in the archive. |

### status.outboxes[]

One outbox claim and the target's frames in it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusoutboxes--claimname"></span>`claimName` | string | yes | The `PersistentVolumeClaim`. |
| <span id="statusoutboxes--pending"></span>`pending` | integer | yes | The target's frames in the claim. |

### status.exposure

The frame in flight. The operator writes it before the exposure starts and clears it after the exposure ends, so an operator that restarts during an exposure counts the frame once.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusexposure--frameid"></span>`frameID` | string | yes | The frame's `FRAMEID`, a UUIDv7. |
| <span id="statusexposure--sequence"></span>`sequence` | integer | yes | The index of the frame's sequence in `spec.sequences`, from 0. |
| <span id="statusexposure--reservation"></span>`reservation` | string | yes | The `Reservation` that takes the frame. |
| <span id="statusexposure--file"></span>`file` | string | yes | The frame's file, relative to the outbox's root, such as `east-main/east-tonight/m42/M42/2026-10-12/Luminance/M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7.fits`. |
| <span id="statusexposure--starttime"></span>`startTime` | string | yes | When the operator started the exposure. |

### status.centering

The last result of centering.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscentering--reservation"></span>`reservation` | string | yes | The `Reservation` that centered. |
| <span id="statuscentering--time"></span>`time` | string | yes | When the last round ended. |
| <span id="statuscentering--rounds"></span>`rounds` | integer | yes | The rounds of solving and slewing it took. |
| <span id="statuscentering--solution"></span>`solution` | [object](#statuscenteringsolution) | no | Where the last solved frame points. Absent when no frame solved. |
| <span id="statuscentering--error"></span>`error` | number | no | The angle between the solved center and `spec.coordinates`, in arcseconds. Absent when no frame solved. |

#### status.centering.solution

Where the last solved frame points. Absent when no frame solved.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscenteringsolution--center"></span>`center` | [object](#statuscenteringsolutioncenter) | yes | The center of the frame, in J2000. |
| <span id="statuscenteringsolution--rotation"></span>`rotation` | number | yes | The angle of the frame on the sky, in degrees. |
| <span id="statuscenteringsolution--scale"></span>`scale` | number | yes | The plate scale, in arcseconds per pixel. |

#### status.centering.solution.center

The center of the frame, in J2000.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscenteringsolutioncenter--ra"></span>`ra` | string | yes | The right ascension in hours, as `HH:MM:SS.s`. |
| <span id="statuscenteringsolutioncenter--dec"></span>`dec` | string | yes | The declination in degrees, as `±DD:MM:SS.s`. |

### status.conditions[]

One condition, in the shape of `metav1.Condition`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name, such as `Complete`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition holds: `True`, `False`, or `Unknown`. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition reflects. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word in CamelCase for the cause. |
| <span id="statusconditions--message"></span>`message` | string | yes | The cause, for a person to read. It can be empty. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When `status` last changed. A write with the same status keeps this time. |
