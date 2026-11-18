---
title: Target
weight: 100
toc: true
---

<!-- Generated from deploy/targets-crd.yaml by crdref. Do not edit. -->

# `Target`

A `Target` is an object in the sky and the frames to take of it, written as sequences of a filter, an exposure, and a count. `astrophotography-operator` images the target during each `Reservation` in the same namespace whose `spec.holder` is `astrophotography-operator`, and then moves each frame to the claim in `spec.archive`. The target stays until you delete it, and `status.sequences` keeps its progress from one night to the next. The operator adds the finalizer `astrophotography.liken.sh/frames` to each target. When you delete a target that still has frames in an outbox, the delete waits until promotion has moved those frames to the archive.

## spec

The object to image, its coordinates, the frames to take, and where to store them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--object"></span>`object` | string | yes | The name of the object, such as `M42`. The operator writes it to the `OBJECT` keyword in each frame's FITS header, and `{object}` in `spec.archive.format` expands to it in each file name. It can hold only printable ASCII characters and no `/`. |
| <span id="spec--coordinates"></span>`coordinates` | [object](#speccoordinates) | yes | Where the object is, in J2000 (ICRS), the form that catalogs such as SIMBAD and KStars print. Before each slew, the operator converts the coordinates to the equinox of the current date, because an INDI mount takes `EQUATORIAL_EOD_COORD` in that equinox. |
| <span id="spec--priority"></span>`priority` | integer | no | When a reservation can image several targets, it picks the one with the highest priority. Among targets of equal priority, it picks the oldest, and then the first by name. Optional. The default is `0`. Default: `0`. |
| <span id="spec--suspend"></span>`suspend` | boolean | no | When `true`, no reservation images the target, and the target keeps its progress. This works like `spec.suspend` of a `Job`. Optional. The default is `false`. |
| <span id="spec--opticaltrain"></span>`opticalTrain` | string | no | The name of the `OpticalTrain` to image through. Optional. A target with an optical train is imaged only on that train's telescope. A target without one is imaged on any telescope whose session has exactly one imaging camera. |
| <span id="spec--sequences"></span>`sequences` | [\[\]object](#specsequences) | yes | The frames to take. The operator finishes the first sequence that still needs frames before it starts the next one, because each filter change needs a refocus. A frame counts toward a sequence when its header has the same filter, exposure, binning, gain, and offset. When you edit this list, frames that still match a sequence stay counted. |
| <span id="spec--dither"></span>`dither` | [object](#specdither) | no | How PHD2 shifts its lock position between frames, so that fixed-pattern noise of the sensor does not add up when the frames are stacked. Optional. Without `dither`, the operator does not dither. |
| <span id="spec--centering"></span>`centering` | [object](#speccentering) | no | How the operator centers the target after a slew. It takes a frame, plate-solves it with the `Solver`, syncs the mount to the solved position, and slews again until the error is within `tolerance`. Optional. Without `centering`, the operator slews and does not solve. |
| <span id="spec--archive"></span>`archive` | [object](#specarchive) | yes | Where to store the frames: a claim, a directory in the claim, and a file name pattern. The camera first writes each frame into the reservation's outbox under its final name, and the operator then moves the frame here. The operator also writes a catalog of the frames to `<path>/catalog/<Target>.jsonl`, where `<Target>` is the name of the target. |
| <span id="spec--fitskeywords"></span>`fitsKeywords` | [\[\]object](#specfitskeywords) | no | FITS header keywords to add to this target's frames, such as `PROGRAM`. Optional. A keyword here replaces the value and comment of a keyword with the same name that the `Observatory`, `Telescope`, `Camera`, or `Reservation` declares. The API server rejects a keyword that the camera's driver sets from its own measurements, such as `EXPTIME`, and the keywords `OBJECT` and `FRAMEID`, which this operator writes. |

### spec.coordinates

Where the object is, in J2000 (ICRS), the form that catalogs such as SIMBAD and KStars print. Before each slew, the operator converts the coordinates to the equinox of the current date, because an INDI mount takes `EQUATORIAL_EOD_COORD` in that equinox.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speccoordinates--ra"></span>`ra` | string | yes | The right ascension in hours, as `HH:MM:SS.s`, such as `05:35:17.3`. |
| <span id="speccoordinates--dec"></span>`dec` | string | yes | The declination in degrees, as `±DD:MM:SS.s`, such as `-05:23:28`. A declination north of the equator can leave out the `+`. |

### spec.sequences[]

One kind of frame and how many frames of it to take.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsequences--filter"></span>`filter` | string | no | A filter name from the filter wheel's `FILTER_NAME` property, such as `Luminance`. The driver writes it to the `FILTER` keyword. Optional. Without it, the operator does not change the filter. |
| <span id="specsequences--exposure"></span>`exposure` | string | yes | The length of each frame, as a duration such as `300s` or `500ms`. |
| <span id="specsequences--count"></span>`count` | integer | yes | The number of frames to take. |
| <span id="specsequences--binning"></span>`binning` | integer | no | The binning on both axes. The operator writes it to the camera's `CCD_BINNING` property. Optional. Without it, the camera keeps its current binning. |
| <span id="specsequences--gain"></span>`gain` | integer | no | The gain, in the camera's own units. The operator writes it to the camera's `CCD_GAIN` property. Optional. Without it, the camera keeps its current gain. |
| <span id="specsequences--offset"></span>`offset` | integer | no | The offset, in the camera's own units. The operator writes it to the camera's `CCD_OFFSET` property. Optional. Without it, the camera keeps its current offset. |

### spec.dither

How PHD2 shifts its lock position between frames, so that fixed-pattern noise of the sensor does not add up when the frames are stacked. Optional. Without `dither`, the operator does not dither.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdither--every"></span>`every` | integer | yes | The number of frames to take between dithers. |
| <span id="specdither--pixels"></span>`pixels` | number | yes | The largest distance PHD2 moves the lock position in one dither, in pixels of the guide camera. |
| <span id="specdither--raonly"></span>`raOnly` | boolean | no | When `true`, PHD2 dithers in right ascension only. Use it for a mount that guides in right ascension only. Optional. The default is `false`. |

### spec.centering

How the operator centers the target after a slew. It takes a frame, plate-solves it with the `Solver`, syncs the mount to the solved position, and slews again until the error is within `tolerance`. Optional. Without `centering`, the operator slews and does not solve.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speccentering--solver"></span>`solver` | string | yes | The name of a `Solver` in the target's namespace. |
| <span id="speccentering--tolerance"></span>`tolerance` | number | no | The largest error that still counts as centered, in arcseconds. The error is the angle between the solved center of the frame and `spec.coordinates`. The default is `30`. Default: `30`. |
| <span id="speccentering--exposure"></span>`exposure` | string | no | The exposure of a centering frame, as a duration. When a frame does not solve, the operator takes one more frame with twice this exposure. The default is `5s`. Default: `5s`. |
| <span id="speccentering--filter"></span>`filter` | string | no | The filter for centering frames. Optional. The default is the filter of the first sequence in `spec.sequences`. |
| <span id="speccentering--attempts"></span>`attempts` | integer | no | The maximum number of rounds of solving and slewing. When the target is still not centered after the last round, the operator sets the `Imaging` reason `CenteringFailed`. The default is `5`. Default: `5`. |

### spec.archive

Where to store the frames: a claim, a directory in the claim, and a file name pattern. The camera first writes each frame into the reservation's outbox under its final name, and the operator then moves the frame here. The operator also writes a catalog of the frames to `<path>/catalog/<Target>.jsonl`, where `<Target>` is the name of the target.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specarchive--claimname"></span>`claimName` | string | yes | The name of a `PersistentVolumeClaim` in the target's namespace. It can be the reservation's outbox claim, and then the operator moves each frame with a rename. The frame counts in `status.sequences` come from the catalog in this claim, so you cannot change it after you create the target. To store frames in another claim, create another `Target`. |
| <span id="specarchive--path"></span>`path` | string | no | A directory in the claim, such as `astro`. The operator stores the frames and the catalog under it. The default is the root of the claim. Default: ``. |
| <span id="specarchive--format"></span>`format` | string | no | The path of each frame's file, relative to `path`, without an extension, because the camera driver adds `.fits`. The pattern can use these tokens: `{target}`, `{object}`, `{filter}`, `{exposure}`, `{binning}`, `{gain}`, `{offset}`, `{camera}`, `{telescope}`, `{train}`, `{reservation}`, `{night}`, `{datetime}`, `{n}`, and `{frameid}`. Any other text can use only letters, digits, `.`, `_`, `+`, `-`, and `/`. When the pattern does not contain `{frameid}`, the operator appends `_` and the last 12 hexadecimal digits of the frame's `FRAMEID`, so that each file name is unique. The default is `{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}`. Default: `{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}`. |
| <span id="specarchive--promotion"></span>`promotion` | string | no | When the operator moves frames to the archive. `Continuous` moves them during the night, so the outbox stays small. `AfterReservation` moves them when the reservation that took them ends, so the copies do not load the network while the telescope is imaging. The default is `Continuous`. One of: `Continuous`, `AfterReservation`. Default: `Continuous`. |

### spec.fitsKeywords[]

One keyword: its name, its value, and a comment.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specfitskeywords--name"></span>`name` | string | yes | The keyword, such as `OBSERVER`: 1 to 8 of the capital letters `A` to `Z`, the digits, `-`, and `_`. `INSTRUME` and `TELESCOP` are allowed, because the driver fills them with the names of INDI devices, such as `CCD Simulator`. |
| <span id="specfitskeywords--value"></span>`value` | string | yes | The value, at most 68 characters, the longest text that one 80-column card holds. The driver writes a value that reads as a number, such as `2026` or `-4.5`, as a number, and any other value as quoted text. A value cannot hold `/`, because the driver writes each `/` in a value as `\`. |
| <span id="specfitskeywords--comment"></span>`comment` | string | no | The comment that follows the value on the card. Optional. At most 47 characters, which fit beside a number. |

## status

The state that `astrophotography-operator` reports. Only the operator writes it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the status reflects. |
| <span id="status--progress"></span>`progress` | string | no | The number of frames in the archive over the number of frames to take, summed over every sequence, such as `37/180`. |
| <span id="status--reservation"></span>`reservation` | string | no | The `Reservation` that is imaging the target now. It is empty while no reservation images the target. The operator writes this field when a reservation claims the target, so two telescopes never image the same target at once. |
| <span id="status--failed"></span>`failed` | [object](#statusfailed) | no | The `Reservation` that released the target after a failure such as `NoFilter` or `SlewFailed`. That reservation does not claim the target again until the spec changes, even after the operator restarts. Other reservations can still claim the target. The operator clears this field when the reservation ends. |
| <span id="status--sequences"></span>`sequences` | [\[\]object](#statussequences) | no | The sequences of `spec.sequences`, in the same order, with their frame counts. The frames left to take in a sequence are `count - archived - pending`. When promotion falls behind, a target can end with more frames than `spec` asks for, but never with fewer. |
| <span id="status--outboxes"></span>`outboxes` | [\[\]object](#statusoutboxes) | no | The outbox claims that hold frames of this target that are not yet in the archive. The operator keeps this list so that it still finds those frames after their reservation ends, and adds a finalizer to each claim in it. A delete of the target waits until no listed claim that still exists has `pending` frames. |
| <span id="status--promotion"></span>`promotion` | [object](#statuspromotion) | no | The state of the target's promotion `Job`s. Only one `Job` of a target runs at a time, and each `Job`'s name ends in a number that increases with each new `Job`. |
| <span id="status--exposure"></span>`exposure` | [object](#statusexposure) | no | The frame that the camera is exposing now. The operator writes it before the exposure starts and clears it after the exposure ends. If the operator restarts during an exposure, it uses this field to count the frame once. |
| <span id="status--centering"></span>`centering` | [object](#statuscentering) | no | The result of the last centering. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Complete` is `True` when every sequence has at least its `count` of frames in the archive. `Imaging` is `True` while a reservation images the target. Its reason is the current step, `Slewing`, `Centering`, `Guiding`, or `Exposing`, and its message gives the reservation and the sequence. When `Imaging` is `False`, its reason is one of `Waiting`, `Suspended`, `SequencesDone`, `NoTrain`, `NoFilter`, `SlewFailed`, `CenteringFailed`, `GuidingFailed`, `OutboxFull`, `ExposureFailed`, `Deleting` while a delete of the target waits for promotion, or `BelowHorizon` while the target is below the telescope's horizon. `ArchiveAvailable` is `True` when the last promotion succeeded. It is `False`, with a reason such as `ClaimMissing` or `NameConflict`, when the last promotion failed. |

### status.failed

The `Reservation` that released the target after a failure such as `NoFilter` or `SlewFailed`. That reservation does not claim the target again until the spec changes, even after the operator restarts. Other reservations can still claim the target. The operator clears this field when the reservation ends.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusfailed--reservation"></span>`reservation` | string | yes | The `Reservation` the target failed on. |
| <span id="statusfailed--observedgeneration"></span>`observedGeneration` | integer | yes | The `metadata.generation` of the spec that the failure happened with. When you edit the spec, `metadata.generation` changes and the reservation can claim the target again. |

### status.sequences[]

One sequence and its counts.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussequences--filter"></span>`filter` | string | no | The sequence's filter. |
| <span id="statussequences--exposure"></span>`exposure` | string | yes | The sequence's exposure. |
| <span id="statussequences--count"></span>`count` | integer | yes | The number of frames to take. |
| <span id="statussequences--binning"></span>`binning` | integer | no | The sequence's binning. |
| <span id="statussequences--gain"></span>`gain` | integer | no | The sequence's gain. |
| <span id="statussequences--offset"></span>`offset` | integer | no | The sequence's offset. |
| <span id="statussequences--archived"></span>`archived` | integer | yes | The number of frames of this sequence in the archive's catalog, as the last promotion `Job` reported it. |
| <span id="statussequences--pending"></span>`pending` | integer | yes | The number of frames of this sequence that are in an outbox and not yet in the archive. |

### status.outboxes[]

One outbox claim and the number of this target's frames in it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusoutboxes--claimname"></span>`claimName` | string | yes | The name of the `PersistentVolumeClaim`. |
| <span id="statusoutboxes--pending"></span>`pending` | integer | yes | The number of this target's frames in the claim. |
| <span id="statusoutboxes--left"></span>`left` | integer | no | The number of frames that the last promotion `Job` for this claim left in the outbox, because they were incomplete or could not be moved. The operator starts a new `Job` only when `pending` is greater than `left`, so a failed frame is not tried again until a new frame arrives. After you fix the cause, add the annotation `astrophotography.liken.sh/retry` to the target. When no promotion `Job` of the target is running, the operator sets `left` to 0 and removes the annotation, and the next `Job` tries the frames again. |

### status.promotion

The state of the target's promotion `Job`s. Only one `Job` of a target runs at a time, and each `Job`'s name ends in a number that increases with each new `Job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspromotion--applied"></span>`applied` | integer | yes | The number in the name of the last promotion `Job` whose report the operator has applied to this status. The next `Job` gets the next number, and the operator never applies the report of a `Job` with this number or a lower one again. |

### status.exposure

The frame that the camera is exposing now. The operator writes it before the exposure starts and clears it after the exposure ends. If the operator restarts during an exposure, it uses this field to count the frame once.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusexposure--frameid"></span>`frameID` | string | yes | The frame's `FRAMEID`, a UUIDv7. |
| <span id="statusexposure--sequence"></span>`sequence` | integer | yes | The index of the frame's sequence in `spec.sequences`, from 0. |
| <span id="statusexposure--reservation"></span>`reservation` | string | yes | The `Reservation` that is taking the frame. |
| <span id="statusexposure--file"></span>`file` | string | yes | The path of the frame's file, relative to the root of the outbox, such as `east-main/east-tonight/m42/M42/2026-10-12/Luminance/M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7.fits`. |
| <span id="statusexposure--starttime"></span>`startTime` | string | yes | When the operator started the exposure. |

### status.centering

The result of the last centering.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscentering--reservation"></span>`reservation` | string | yes | The `Reservation` that centered the target. |
| <span id="statuscentering--time"></span>`time` | string | yes | When the last round of centering ended. |
| <span id="statuscentering--rounds"></span>`rounds` | integer | yes | The number of rounds of solving and slewing that the centering took. |
| <span id="statuscentering--solution"></span>`solution` | [object](#statuscenteringsolution) | no | Where the last solved frame points. Not set when no frame solved. |
| <span id="statuscentering--error"></span>`error` | number | no | The angle between the solved center and `spec.coordinates`, in arcseconds. Not set when no frame solved. |
| <span id="statuscentering--rounderrors"></span>`roundErrors` | []number | no | The `error` of each round whose frame solved, in order, in arcseconds. The first value is the distance between the target and where the slew ended, before the first sync corrected the mount. |

#### status.centering.solution

Where the last solved frame points. Not set when no frame solved.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscenteringsolution--center"></span>`center` | [object](#statuscenteringsolutioncenter) | yes | The center of the solved frame, in J2000. |
| <span id="statuscenteringsolution--rotation"></span>`rotation` | number | yes | The angle of the frame on the sky, in degrees. |
| <span id="statuscenteringsolution--scale"></span>`scale` | number | yes | The plate scale, in arcseconds per pixel. |

#### status.centering.solution.center

The center of the solved frame, in J2000.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscenteringsolutioncenter--ra"></span>`ra` | string | yes | The right ascension in hours, as `HH:MM:SS.s`. |
| <span id="statuscenteringsolutioncenter--dec"></span>`dec` | string | yes | The declination in degrees, as `±DD:MM:SS.s`. |

### status.conditions[]

One condition, in the shape of `metav1.Condition`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The type of the condition, such as `Complete`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition is met: `True`, `False`, or `Unknown`. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition reflects. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | A one-word CamelCase reason for the status. |
| <span id="statusconditions--message"></span>`message` | string | yes | A message for a person that gives details of the reason. It can be empty. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When `status` last changed. A write that sets the same `status` keeps this time. |
