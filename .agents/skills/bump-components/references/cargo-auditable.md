# cargo-auditable

`toolchains/cargo-auditable/latest.sh --bump` moves cargo-auditable.
Every Rust image builds with this cargo subcommand, so that each
binary records its crates in a `.dep-v0` section that syft and Trivy
read. "Rust binaries" in the `images` skill describes the form.

The bump writes the version, and the digest from the release's
`.sha256` file, into `toolchains/cargo-auditable/fetch.sh`. The
project does not sign the release file or the `.sha256` file.

Then build each image whose Dockerfile runs `cargo auditable build`.
Check that syft still lists the crates of one of them: this command
must print a number greater than 0.
`docker run --rm -v /var/run/docker.sock:/var/run/docker.sock
anchore/syft:v1.55.0 <image> -o json -q | jq '[.artifacts[] |
select(.type == "rust-crate")] | length'`

The Rust images are tracked components, so no revision changes.
