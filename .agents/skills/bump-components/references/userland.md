# The static programs of userland

The static programs in `liken/userland/` follow buildroot's `master`
branch. Run each command from `liken/`.

- Run `userland/latest.sh --bump`. It moves buildroot to the tip of
  `master`, and writes the same commit and digest into
  `bluetooth-operator/bluetoothd/Dockerfile`. It also deletes each
  line of `userland/versions.mk` whose version the new recipe already
  meets.
- A program row that still shows `behind` then needs a bump by name,
  such as `userland/latest.sh --bump tzdata`. For a program whose
  recipe is one of buildroot's own, the bump sets the newer version in
  `versions.mk`, and writes its `.hash` file under
  `userland/patches/`.
- For a program whose recipe is in the tree, under `userland/iscsi/`
  or `userland/bluetooth/`, the bump writes the version into the
  recipe, and the line of the tarball into the recipe's `.hash` file.
- When the `buildroot` row of `toolchains/latest.sh` shows `behind`
  after the move, run `../toolchains/latest.sh --bump buildroot`.
- A buildroot bump can move every library and the compiler. So the
  bump is not done until `make -C userland` builds. The first build
  after a buildroot bump compiles the cross-compiler again. The
  compiler cache in `userland/cache/ccache/` makes the builds after it
  faster.
