# 20, Declared FITS keywords

Proposed on 2026-10-12, and designed on 2026-10-12. The design below
answers what the design conversation left open, from the evidence of
the CCD simulator. Built on 2026-10-12. Drilled on the QEMU dev
cluster on 2026-10-12; [the drill](#the-drill) records the results,
and a defect of the INDI client that the example's keywords found,
which 5e6ce6e0 fixed.

## The problem

A frame's FITS header is how every later tool knows what the frame is.
`liken`'s astronomy components aim to write the most complete and
correct headers that any imaging program writes. That means the
keywords of the FITS standard, of SBFITSEXT, the extension that SBIG
and Diffraction Limited published for amateur cameras, and of common
practice, which N.I.N.A. documents keyword by keyword. A person also
wants their own keywords on every frame.

## Decisions

- **A header has two kinds of keywords, and the operator treats them
  differently.** The driver measures some at capture: `DATE-OBS`,
  `EXPTIME`, `CCD-TEMP`, the mount's coordinates, the filter. A person
  declares the others, and they do not change from frame to frame:
  `OBSERVER`, `TELESCOP`, the site, and anything custom.
- **Declared keywords overlay down a hierarchy.** The `Observatory`,
  the `Telescope`, the `Camera`, and the `Reservation` can each declare
  keywords, and the most specific level wins.
  `astrophotography-operator` continues the hierarchy with the levels
  it owns, such as the target.
- **A declared value never replaces a measured one.** A header that
  lies is worse than a header that lacks a keyword, so a resource that
  declares a measured keyword is refused when it is applied, not
  quietly overridden.
- **A driver that measures wrongly is a bug to report upstream.** The
  operator does not patch over drivers.
- **The driver writes every keyword when it creates the file.** The
  operator sets the declared keywords on the camera through its
  `FITS_HEADER` property before an exposure. No component edits a
  frame after the driver writes it, so a frame is complete and final
  from the moment it exists. Frames in mode 1 carry the keywords too.
- **Where the conventions disagree, the person chooses.** For example,
  `IMAGETYP` is `Light Frame` in SBFITSEXT and `LIGHT` in the IRAF
  style.

## What the driver does

On 2026-10-12, the CCD simulator of the pinned `indi-simulators` image
(`20261005-5`) ran in Docker on the laptop with the mount, focuser,
rotator, filter wheel, and SQM simulators, and a client wrote
`FITS_HEADER` between exposures and read the headers of the files that
the driver saved:

- **A keyword stays.** One write puts the keyword on every later frame,
  until the driver process exits. A disconnect and a connect do not
  clear it.
- **A second write of the same name replaces the value**, and a new
  name adds a keyword.
- **One write removes every keyword.** A write whose name is
  `INDI_CLEAR` removes every keyword that a client wrote, and a write
  with an empty value changes nothing. The first probe tried only the
  empty value, and the design first said that no keyword can be
  removed. The build found `INDI_CLEAR` in libindi's
  `libs/indibase/indiccd.cpp`, and a second probe on the same image
  confirmed it: the frame after the clear held only the driver's own
  keywords.
- **The driver changes a value that holds `/`.** It writes each `/` in
  a value as `\`, so `f/5` reached the header as `f\5`. A comment kept
  its `/`.
- **A written keyword replaces the driver's own.** A write of `EXPTIME`
  as 999 put `EXPTIME = 999` on the next frame, in place of the
  exposure's real length. The driver does not protect what it
  measures, so the operator must.
- **The driver infers the type.** A value that reads as a number is
  written as a number, and any other value is written as a quoted
  string.
- **The driver writes these keywords**, with every simulator connected:
  the structure of the file (`SIMPLE`, `BITPIX`, `NAXIS`, `NAXIS1`,
  `NAXIS2`, `EXTEND`, `BZERO`, `BSCALE`, `ROWORDER`), the names of the
  INDI devices (`INSTRUME`, `TELESCOP`), and the measures of the
  exposure: `EXPTIME`, `CCD-TEMP`, `PIXSIZE1`, `PIXSIZE2`, `XBINNING`,
  `YBINNING`, `XPIXSZ`, `YPIXSZ`, `FRAME`, `IMAGETYP`, `FILTER`,
  `FOCALLEN`, `APTDIA`, `ROTATANG`, `FOCUSPOS`, `FOCUSTEM`, `SCALE`,
  `SITELAT`, `SITELONG`, `AIRMASS`, `OBJCTAZ`, `OBJCTALT`, `OBJCTRA`,
  `OBJCTDEC`, `RA`, `DEC`, `PIERSIDE`, `EQUINOX`, `DATE-OBS`, and
  `GAIN`. The header is in `indi/testdata/ccd/frame-header.txt`.
  libindi's CCD also writes `OFFSET`, `DARKTIME`, and `MPSAS` when the
  camera or an SQM reports them, the Bayer keywords, `DATAMIN`, and
  `DATAMAX` for some cameras and frames, and the WCS keywords when it
  writes a world coordinate system; the simulators did not.

## The design

- **The field is `spec.fitsKeywords`** on the `Observatory`, the
  `Telescope`, the `Camera`, and the `Reservation`: a list of `name`,
  `value`, and an optional `comment`, keyed by `name`. The CRD marks
  it a list of type `map`, so server-side apply merges one keyword at a
  time, and the header keeps the order the person wrote.
- **The schema refuses a keyword the driver owns.** The CRD's rules
  refuse a name that is not a FITS keyword (one to eight of `A`-`Z`,
  `0`-`9`, `-`, and `_`), a name on the driver's list above, except
  `INSTRUME` and `TELESCOP`, and a value that holds `/`, which the
  driver would change. The API server refuses such a
  resource when it is applied, so a mistake never waits for a night to
  show. `INSTRUME` and `TELESCOP` are declarable, because the driver
  fills them with the names of INDI devices, such as `CCD Simulator`,
  which name a driver and not the person's equipment. `COMMENT`,
  `HISTORY`, and `END` are refused too, because they are not
  keywords with values.
- **The most specific level wins.** The operator merges the four lists
  in the order `Observatory`, `Telescope`, `Camera`, `Reservation`, and
  a name at a later level replaces the value of the same name at an
  earlier one.
- **`Configure` clears the keywords and writes the merged list once**,
  one write of `INDI_CLEAR` and then one write of `FITS_HEADER` for
  each keyword, to each imaging camera of the session. A guide camera
  writes no files, so it gets none. Because a keyword stays until the
  driver exits, one write covers every frame of the session, including
  the frames of a desktop client in mode 1. The clear comes first even
  when the list is empty, so a retry that removes a keyword removes it
  from the driver.
- **An edit takes effect at the next session.** The session records the
  merged list when `PowerOn` records the devices, as [plan
  17](completed/17-a-session-freezes-its-devices.md) does for every
  other spec, so the frames of one night agree with each other.
- **A choice of convention belongs to whoever sets the frame type.**
  The driver writes `IMAGETYP` in the SBFITSEXT form, `Light Frame`,
  from the frame type of the exposure. `IMAGETYP` is on the driver's
  list, so a person cannot declare it. A program that sets the frame
  type, such as `astrophotography-operator`, can write the form the
  person chose for that frame type, because it writes the fact the
  driver measures in another spelling and not a different fact.

## The drill

The drill ran on 2026-10-12 on the QEMU dev cluster, on the lab of
[plan 19's drill](19-the-frames-outbox.md#the-drill), with the
observatory's pods on `node-4`. The operator was built from d8d0759a
and reached the lab through the lab registry as
`ghcr.io/liken-sh/observatory-operator:dev`. The observatory was a copy
of `examples/simulators.yaml` without its `Reservation`, so the
`Observatory` declared `OBSERVER` and `Telescope east` declared
`TELESCOP`. The drill added `INSTRUME` and `OBJECT` (`Camera default`)
to `Camera east-main`. Each reservation of `east` declared `OBJECT`
and named the outbox `frames`, a `local-path` claim on `node-4`, so
the frames landed where a `busybox` pod could read them. The exposures
were 1 s, through a port forward to `east-telescope`.

The guider could not run on this lab. Its pod runs beside the
telescope's server on `node-4`, whose image filesystem had 450 MiB
free, and the images of PHD2 and weston take about 785 MB when
unpacked. So the drill reserved `east` once with `Guider east` and
ended the reservation during `Activation`, before `StartGuider`. It
then deleted `Guider east`, so `Camera east-guide` was an imaging
camera in the other sessions.

| Check | Result |
|---|---|
| Write `EXPTIME` and `CRVAL1` on a `Telescope` and a `Reservation`, `DATE-OBS` on a `Camera`, and `CCD-TEMP` on the `Observatory` | Each refused, for example: "EXPTIME is a keyword that the camera's driver writes from what it measures, and a declared value would replace the measured one". The `Reservation`'s hand-written copy of the field gave the same messages as the generated CRDs. |
| Write the value `f/5`, and `a/b` on a `Reservation` | Refused: "the camera's driver writes each '/' in a value as '\', so the header would not hold the value declared". |
| Write `COMMENT`, `observer`, and `OBJECT` twice on a `Telescope` | Refused: "COMMENT is a keyword that FITS reserves for a card with no value", "a FITS keyword name is 1 to 8 of the capital letters A to Z, the digits, '-', and '_'", and "Duplicate value: {"name":"OBJECT"}". |
| Write `TELESCOP`, `INSTRUME`, and `OBJECT` | Accepted. |
| Reserve `east` with `Guider east` and `OBJECT` `M31` | `status.session.fitsKeywords` held `Camera/east-main` alone, the guide camera's none: `OBSERVER`, `TELESCOP`, `INSTRUME`, and `OBJECT` `M31`, the reservation's value over the camera's. `Configure` failed after its deadline of 2 min, as the next paragraphs describe. |
| Change the comment of `TELESCOP`, and annotate `observatory.liken.sh/retry=1` | The retry recorded the session again with the new comment, and `Configure` took 1 s. |
| Reserve `east` with no `Guider` | `Ready` 35 s after creation. `Configure` took 1 s. The record held both cameras, and `Camera/east-guide` had no `INSTRUME`, the camera's keyword. |
| Expose `CCD Simulator`, and read `IMAGE_003.fits` from the claim | `INSTRUME = 'East main camera' / the Camera level` in the place of the driver's `CCD Simulator`, `OBJECT = 'M31'`, `OBSERVER = 'Simulated Observer'`, and `TELESCOP = 'East refractor'`. The header held one card for each name, so the driver's own `TELESCOP` was gone. |
| Expose `Guide Simulator` | `INSTRUME = 'Guide Simulator' / Camera Name`, the driver's, and the same `OBJECT`, `OBSERVER`, and `TELESCOP`. |
| Change `OBSERVER` on the `Observatory` to `Edited Observer` and `OBJECT` on the `Reservation` to `M33` while the reservation is `Ready`, and expose | 15 s later, the record was the same, and the camera pod had the same UID and a `restartCount` of 0. `IMAGE_005.fits` held `Simulated Observer` and `M31`. |
| Delete the pod `east-main-camera`, and expose | The `Camera` was `Connected` again 5 s after the delete, in a new pod. `IMAGE_006.fits`, from the new driver process, held the four recorded keywords. |
| End the reservation, reserve `east` again, and expose | `Ready` 36 s after creation. The record held `Edited Observer` and `M33`, and so did `IMAGE_007.fits`. |

The first reservation failed because of the comment in the example,
"the Telescope's own name". The operator's INDI client writes a text
with `xml.EscapeText` (`indi/wire.go`), which writes `'` as `&#39;` and
`"` as `&#34;`. libindi's XML parser decodes the named entities
`&apos;`, `&quot;`, and `&amp;`, but it does not decode a numeric
character reference. So the driver stored the comment as the literal
text `the Telescope&#39;s own name`, and answered with that text.
`Settle` waits until the driver answers with the values sent, so it
waited until the step's deadline, and the `Camera`'s Warning `Event`
said "writing the FITS keyword TELESCOP: Camera east-main: context
deadline exceeded".

Two probes isolated the cause. A raw client wrote `it's`, `it&apos;s`,
and `it&#39;s` as a comment. The driver answered the first two with
`it's`, and the third with the literal text `it&#39;s`, which it sent
as `it&amp;#39;s`. The operator's own client,
against the same server, settled `INDI_CLEAR` in 43 ms and `OBSERVER`
in 2 ms, and did not settle `TELESCOP` with the example's comment in
8 s. The defect is in the INDI client, and it affects every text that
the client writes with a `'` or a `"`, not only a FITS keyword. The
drill did not fix it. It changed the comment on the lab's `Telescope`
to "the name of the Telescope" and ran the other checks with that
comment.

Commit 5e6ce6e0 fixed the client: it writes `'` as `&apos;` and `"` as
`&quot;`. A second drill on the same lab, on the same day, built the
operator from 5e6ce6e0 and restored the example's comment on
`Telescope east`. The reservation of `east` declared `OBJECT` with the
value `the "Orion" nebula` and named the outbox `frames`. It was
`Ready` 37 s after creation, and `Configure` took 1 s. A 1 s exposure
of `CCD Simulator` wrote `IMAGE_008.fits` with these cards:

```
OBJECT  = 'the "Orion" nebula' / the Reservation level
TELESCOP= 'East refractor'     / the Telescope's own name
```

FITS doubles a `'` only inside a value, so the comment kept its single
`'`, and the value kept each `"` as declared. The reservation was
released 67 s after its delete.

## Still open

`astrophotography-operator` writes keywords of its own levels, such as
`OBJECT` for each target, into the same running driver. `INDI_CLEAR`
removes every keyword, the ones `observatory-operator` wrote included,
so after each clear, `astrophotography-operator` writes the session's
list from `status.session.fitsKeywords` again with its own. Its [plan
01](../../astrophotography-operator/plans/01-run-a-targets-sequence.md)
has to account for this.

The change of `/` to `\` in a value is worth a report to libindi.

Keywords written into archived frames are the hardest decision to
change later, because the frames never change. The keyword list
deserves the most care of anything in this plan.
