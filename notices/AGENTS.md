# Working on notices

This directory holds the scripts that put the license notices of the
third-party software in an image into that image. `README.md` explains
what each script writes and why. Every image of an operator takes this
directory as the build context `notices`, so a change to a script here
rebuilds each of those images. Build one of them through
`docker-bake.hcl` at the top of the repository and run its smoke check
before you commit a change here.

@../brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included.

The notices of Debian and Ubuntu packages are not here. Every closure
writes them with the `notices` function of `vulkan/closure.sh`, the
closure library, and `indi/closure.sh` is a byte-for-byte copy of it.
