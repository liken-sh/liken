# The functions of `toolchains/pin.sh`

Every `fetch.sh` and `latest.sh` sources `toolchains/pin.sh`, which
has one implementation of each step of a pin. The comment above each
function in `pin.sh` gives its arguments and the reason for its
behavior.

- **`require_tools`** stops when a tool is missing. **`fail`** stops
  with a message that names the script.
- **`fetch_text`, `url_exists`, `newest_tag`, `tag_commit`,
  `github_api`, and `registry_digest`** read from an upstream, with
  the retries and time limits that every read uses. `github_api`
  sends `GITHUB_TOKEN`, or the token of the gh CLI when one is
  available.
- **`fetch_verified [--resume] <url> <digest> <path>`** puts a pinned
  file in the cache. It keeps a cached file that matches the digest.
  It downloads anything else into a `.partial` file. When the
  `.partial` file does not match, it deletes the file and stops. When
  the file matches, it moves the file into place. Use `--resume` for
  a large file. The digest is the one that the script pins, and an
  empty digest stops the fetch.
- **`fetch_pinned [--resume] <entry> <version> <path>`** fetches the
  file that an entry of the `pin.toml` beside the script lists,
  through `fetch_verified`. It stops when the entry has another
  version than the one that the fetch builds.
- **`pin_value <entry> <field> [<manifest>]`** prints one field of a
  manifest. **`set_pin <entry> <version> <url> <sha256> <purl>`**
  writes a bump into one entry. **`generic_purl`, `github_purl`, and
  `deb_purl`** print the purl of each kind.
- **`fetch_file`** downloads a file whose digest is not yet in the
  repository, such as a checksum file or a signature.
  **`digest_of_url`** prints the sha256 of a download, for an upstream
  that publishes no checksum.
- **`verify_signature [--signer <fingerprint>] <key.asc> <signature>
  [<file>]`** checks a detached signature. Given a clearsigned file,
  it checks the file and prints its signed text.
- **`archive_deb <key.asc> <suite URL> <index> <package> [<version>]`**
  prints the version and the sha256 of one package of a Debian or
  Ubuntu archive. It gets them through the suite's signed `InRelease`
  file and the `Packages` index that `InRelease` lists.
- **`launchpad_published`, `ubuntu_development_series`, and
  `ubuntu_newest_in`** read Launchpad's API for an Ubuntu source
  package. **`ubuntu_deb_digest`** gets the digest of one of its
  packages through `archive_deb`, checked against
  `trust/keys/ubuntu-archive.asc`. **`ubuntu_source_files`** gets the
  digests of the files of the source package through `archive_source`
  and the suite's Sources index. **`set_ubuntu_source_pins`** writes
  those digests into the manifest, with the Launchpad URL of each
  file.
- **`container_runtime`** prints docker or podman, whichever is
  installed, for a build to run, and **`image_of <key>`** prints the reference of an image from
  `toolchains/images.toml`.
- **`row`** prints one report row of seven columns, with `?` when the
  latest version is empty.
- **`bump_needed`, `bumped`, and `next_step`** print the lines of a
  bump. **`set_line`, `set_var`, `set_toml`, `set_entry`, and
  `add_entry`** write a bump into the file that has the pin. Each one
  stops when its pattern matches no line or more than one line, so a
  bump never reports a change that it did not write.
