---
name: observatory-schema
description: Changes the schema of observatory-operator's observatory.liken.sh resources, and records the INDI captures again after a bump of the INDI images. Covers the CRDs written by hand and the ones that schema/ generates, the templates and the fields files, the standard INDI properties that the typed fields come from, make schema and make crosscheck, the drift, enum, validation, and example tests, and the order of make drivers, make firmware-rules, make captures, the frame header capture, make schema, and make crosscheck after a bump. Use when adding, renaming, or removing a field or a kind of observatory.liken.sh, when editing a CRD, a template, or a fields file in observatory-operator, when a schema test fails as stale, and after a bump of the indi images.
---

# The observatory.liken.sh schema

Each kind of `observatory.liken.sh` has a Go type in
`observatory-operator/observatory/` and a CRD in
`observatory-operator/deploy/`. `schema/` generates the CRD of each
kind with procedures, from its template
`schema/<plural>-crd.yaml.tmpl`. Those CRDs share parts, and the
generator gives each shared part one source. A CRD with no template
is written by hand, such as the CRD of `Reservation`. Run the
commands below from `observatory-operator/`.

## The sources of a generated CRD

- `<plural>-crd.yaml.tmpl` is the CRD of one kind. It writes out the
  fields that only that kind has. The kind's own action fields go in
  its template block named `action`.
- `device.yaml.tmpl` has the fields that every device kind shares,
  `DeviceSpec` and `DeviceStatus`. `procedures.yaml.tmpl` has the
  procedure schema: `spec.activation`, `spec.deactivation`,
  `spec.triggers`, and `status.procedures`.
- `fitskeywords.yaml.tmpl` has `spec.fitsKeywords`, which the
  templates of several kinds include, such as the `Camera`'s. The
  field's rule rejects the keywords that the camera's driver writes
  itself. The generator writes the names of those keywords from
  `observatory/fitskeywords.go`, which is the only list of them. The
  `Reservation`'s CRD is written by hand, and it has a copy of the
  field. `TestEachLevelHasTheSameFITSKeywordsSchema` fails when the
  copy differs from the generated field.
- `<plural>-fields.yaml` lists the typed fields of a device kind. It
  has one entry for each standard property of the kind's INDI
  interfaces. Each entry gives the field's name, its role, the unit of
  each number, and its description. The role decides whether the
  field is in both spec and status, or in status only. The package doc
  comment in `schema/curated.go` describes the format. The generator
  writes the fields into the CRD, and their Go types into
  `observatory/generated_*.go`.

The standard properties of each interface come from `indi/standard/`.
The captures of the bare drivers give the structure of each property.
`conventions.yaml` adds the properties that drivers share but
libindi does not define, such as `CCD_GAIN`. The generator fails in
each of these cases:

- a standard property that no fields file names
- an entry that names no standard property
- an entry or a member with no description
- a description that does not name its unit

The descriptions are the project's own text, written in the voice of
`brand/voice.md`, because `kubectl explain` and the manual publish
them. The standard properties page of `indilib/docs` is only a
cross-check. The generator never reads it, because its text is not
the project's to publish.

## Add or change a field or a kind

1. Check that the field does not exist. Search the kind's fields file
   and the kind's CRD for the INDI property or the field's name, for
   example for the temperature of a focuser:

   ```sh
   grep -n -i temperature schema/focusers-fields.yaml deploy/focusers-crd.yaml
   ```

   A fields file has an entry for each standard property of the kind's
   INDI interfaces. So each standard property already has a field,
   such as `temperature` for `FOCUS_TEMPERATURE`. A request to add the
   field of a standard property is usually a request to change the
   entry's role, its unit, or its description. Change the entry.
2. Edit the template or the fields file. For a kind that is written by
   hand, edit its CRD and its Go type. Give a new field a description
   that names its unit.
3. Run `make schema`. It writes the generated CRDs and Go types
   again. It fails on each standard property that no fields file
   names. `TestTheSchemaIsCurrent` fails when a generated file is
   stale.
4. Run `make -C docs generate skills`, because the manual's reference
   pages are generated from the CRDs.
5. Run `make test`.

The tests in `observatory/` load the CRDs into the API server's own
validators, and compare them with the Go types:

- `drift_test.go` compares each Go type with its CRD, field by field.
  It fails on a field that is on one side only, or a field whose type
  differs.
- `enum_test.go` fails when a CRD enum differs from the Go constants.
- `validation_test.go` runs resources that a person could write
  through the structural schema and the CEL rules.
- `example_test.go` checks `examples/simulators.yaml` against the
  CRDs, the Go types, and itself: every name that the example refers
  to must exist in it. Most of the operator's tests use the example
  as their inventory, so a change to the example changes those tests.

A fake API server cannot prove a CRD schema or a CEL rule. Prove it
on a cluster. The `dev-cluster-drills` skill has the drill.

`make crosscheck` fetches the standard properties page of
`indilib/docs` from GitHub, at the newest commit or at `COMMIT`. It
writes `schema/disagreements.txt`, which lists each place where these
sources disagree: the page, the captures, the conventions, the
simulators, and the units of the fields files. The file also records
the commit of the page. The command needs access to GitHub, so you
run it on a workstation, and no test runs it. A test in `schema/`
fails when a line of the file names a property that no source
defines.

## After a bump of the INDI images

The `bump-components` skill bumps `../indi/package.toml`, the file of
the `indi` component at the top of the repository. In this section,
a path that starts with `../indi/` is in that component. A path that
starts with `indi/` is in `observatory-operator/indi/`, the operator's
INDI client and its captures.

After the bump, update the operator's generated files and its
recorded captures for the new tag and the new libindi. Each `make`
target here that runs Docker builds its image through
`docker-bake.hcl` at the top of the repository. So these targets run
on a workstation, never in CI. The frame header is captured by hand
between `make captures` and `make schema`, so one `make` command
cannot run every step. Run the steps in this order:

1. `make drivers` writes the new tag into `drivers/generated.go`, the
   map from each driver to the image that contains it. It reads the
   lists in `../indi/images/`, the tag in `../indi/package.toml`, and
   the tag of the `weston` image in `../weston/package.toml`.
   `TestTheMapIsCurrent` fails when the file is stale.
2. `make firmware-rules` builds `indi-qhy`, copies `libqhy`'s udev
   rules out of it into `firmware/85-qhyccd.rules`, and runs
   `make firmware`. `make firmware` writes `deploy/firmware/qhy.yaml`:
   one `Firmware` for `firmware-operator` for each bare QHY chip that
   the image's `fxload` loads, on the `indi-qhy` tag of
   `drivers/generated.go`. `TestTheQHYFirmwareIsCurrent` fails when
   the file is stale. Report each object that this step adds or
   removes. `TestTheCommittedObjectsAreWellFormed` in
   `firmware/generate_test.go` compares the number of objects with a
   number written in the test. When this step adds or removes an
   object, change that number to the count of objects in
   `deploy/firmware/qhy.yaml`.
3. `make captures` runs two targets. `make standard` builds the bare
   drivers of `indi/standard/bare/` against the libindi of the pinned
   snapshots, and captures each one into `indi/standard/`.
   `make transcripts` runs each simulator of `indi-simulators` through
   its script, into `indi/inditest/testdata/`.
   `TestCapturesNameThePinnedINDI` fails when a capture names an INDI
   version other than the one in `../indi/package.toml`. So the
   operator's tests fail until this step runs.
4. Capture the frame header again, by hand, as "The frame header"
   below describes.
5. Run `make schema`. It fails on each new standard property until a
   fields file gives that property a field and a description.
6. Run `make crosscheck`. Report each new line of
   `schema/disagreements.txt`.
7. Run `make -C docs generate skills`, because the manual's reference
   pages are generated from the CRDs that `make schema` wrote.
8. Run `make test`.

### The bare drivers

A bare driver is a small C++ driver for one INDI interface. It turns
on every capability of the interface and defines no properties of its
own. Each capture in `indi/standard/` contains every property that
one bare driver defined. The properties of `INDI::DefaultDevice` and
of the connection plugins are only in `device.xml`, and every other
capture leaves them out.

The bare drivers build from their own bake file,
`indi/standard/bare/docker-bake.hcl`. They need a separate file
because `ci/` publishes every target of the bake file that it writes,
and the bare drivers are never published. Their sources are in
`observatory-operator` and not in `indi/`, because a change to a file
in `indi/` changes the recipe of every INDI image.

### The frame header

`indi/inditest/testdata/ccd/frame-header.txt` contains the 80-column
header cards of a frame that the CCD simulator saved, with the
trailing spaces removed. `TestTheDriversKeywordsAreRefused` in
`observatory/fitskeywords_test.go` compares the driver's list of
keywords, `DriverFITSKeywords`, with the file. So when a new capture
after a bump has a new keyword, the test fails until the list names
it.

`make transcripts` does not write this file. To capture it, you run
the simulators on one server and read the file that the driver saved.
The functions of `../indi/smoke/lib.sh` run the server and set each
property. `serve` starts `indiserver` in the image with the drivers
that you name. `setprop` and `getprop` run `indi_setprop` and
`indi_getprop` in the container with `docker exec`.
`../indi/smoke/simulators.sh` uses the same functions to take a frame.
`make transcripts` loads the `indi-simulators` image at the pinned
tag, so run these commands after step 3:

1. Start the telescope, focuser, rotator, filter, SQM, and CCD
   simulators on one server:

   ```sh
   . ../indi/smoke/lib.sh
   tag=$(sed -n 's/^version = "\(.*\)"/\1/p' ../indi/package.toml)-$(sed -n 's/^revision = \(.*\)/\1/p' ../indi/package.toml)
   name=$(serve ghcr.io/liken-sh/indi-simulators:$tag indi_simulator_telescope \
       indi_simulator_focus indi_simulator_rotator indi_simulator_wheel \
       indi_simulator_sqm indi_simulator_ccd)
   ```

2. List the devices, and connect each one. The names come from the
   drivers, such as `Telescope Simulator` and `CCD Simulator`:

   ```sh
   getprop "$name" '*.CONNECTION.CONNECT'
   setprop "$name" "<device>.CONNECTION.CONNECT=On"
   ```

3. Unpark the mount and slew it:

   ```sh
   setprop "$name" "Telescope Simulator.TELESCOPE_PARK.UNPARK=On"
   setprop "$name" "Telescope Simulator.ON_COORD_SET.TRACK=On"
   setprop "$name" "Telescope Simulator.EQUATORIAL_EOD_COORD.RA;DEC=5.588;-5.39"
   ```

4. Write `SCOPE_INFO`, set `UPLOAD_MODE` to `UPLOAD_LOCAL`, and write
   the one `FITS_HEADER` keyword `OBJECT`:

   ```sh
   setprop "$name" "CCD Simulator.SCOPE_INFO.FOCAL_LENGTH;APERTURE=400;80"
   setprop "$name" "CCD Simulator.UPLOAD_SETTINGS.UPLOAD_DIR;UPLOAD_PREFIX=/tmp;IMAGE_XXX"
   setprop "$name" "CCD Simulator.UPLOAD_MODE.UPLOAD_LOCAL=On"
   setprop "$name" "CCD Simulator.FITS_HEADER.KEYWORD_NAME;KEYWORD_VALUE;KEYWORD_COMMENT=OBJECT;M42;declared"
   ```

5. When `getprop "$name" -1 'Telescope Simulator.EQUATORIAL_EOD_COORD._STATE'`
   prints `Ok`, take an exposure of 1 s:

   ```sh
   setprop "$name" "CCD Simulator.CCD_EXPOSURE.CCD_EXPOSURE_VALUE=1"
   ```

6. Copy the header of the file that the driver saved into
   `frame-header.txt`, with the trailing spaces removed. Then stop the
   server:

   ```sh
   docker cp "$name:/tmp/IMAGE_001.fits" - | tar -xO | fold -w 80 \
       | sed -n '1,/^END *$/p' | sed 's/ *$//' > indi/inditest/testdata/ccd/frame-header.txt
   stop "$name"
   ```
