# The INDI images

`indi` is also a pinned component. It has the images that
`observatory-operator` runs, and `indi/package.toml` lists them. It is
not one of the bases of section 5 of `SKILL.md`. It installs from
Ubuntu, the INDI PPA, and the PHD2 PPA, through a dated snapshot of
each, and it moves on its own schedule. `indi/snapshot.sh` names the
Ubuntu series.

The `version` of `indi` is the date of those snapshots. Its
`[package.upstream]` lists the versions of the packages that matter,
such as `indi-bin` and `phd2`, that the date installs. Its smoke checks fail when the image has other versions.

`indi/latest.sh` reports the snapshot date, the versions of
`[package.upstream]`, and the newest Ubuntu series that both PPAs publish. When a
PPA publishes a newer series, the row shows `wrong channel`. To move
to that series, edit `indi/snapshot.sh` and the `ubuntu` entry of
`toolchains/images.toml` by hand.

`indi/latest.sh --bump` does these steps:

- It sets the date and revision 1.
- It updates `[package.upstream]` from the snapshot's `Packages`
  index.
- It writes the digest of the `InRelease` file of each suite into
  `indi/snapshot.sha256`. After a move to a new series, the suites in
  `indi/snapshot.sha256` have the names of the new series.

Then run `make workflows`.

A new snapshot can bring a new SDK, or an SDK whose license file
changed. Compare the license file of each SDK directory in
indi-3rdparty, at the commit that the PPA built from, with
`indi/sdk-licenses/`. Copy each new or changed license file, and add
a `license` entry to the list of the image. Then write the commit
into `indi/AGENTS.md`, which records the commit that `sdk-licenses/`
mirrors.

Build every target in `indi/package.toml`, and run each smoke check.
A short script can do both:

1. Read the `target` and `smoke` of each `[[outputs.images]]` entry
   in `indi/package.toml`.
2. Run `make bases`.
3. Run `docker buildx bake <targets> --load`.
4. Run each smoke script on
   `ghcr.io/liken-sh/<target>:<version>-<revision>`.

The build fails when a new release adds a third-party driver that no
list in `indi/images/` includes. Add the driver to the list of an
image, or to `indi/images/unpublished`, and say which in the report.

Also search the new closure for the libraries that a driver loads
with `dlopen`, because `ldd` does not list them. `objdump -T` shows
the libraries that import `dlopen`, and running a driver with
`LD_DEBUG=libs` shows what it opens. For this reason, the alignment
math plugins and the gphoto2 plugins are seeds in their lists: files
that the closure starts from, together with everything they load.

Then follow the section "After a bump of the INDI images" of the
`observatory-schema` skill. It runs
`make -C observatory-operator drivers firmware-rules captures`. Then
you capture the frame header by hand. Then it runs
`make -C observatory-operator schema crosscheck`.

- Report each object that `firmware-rules` adds to or removes from
  `deploy/firmware/qhy.yaml`. A new object is a new QHY camera that
  `firmware-operator` loads, and the test that counts the objects
  needs the new count.
- Report each new line of
  `observatory-operator/schema/disagreements.txt`.

CI builds the images of `indi` only when this tag is new.
