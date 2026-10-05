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

The pinned components, `vulkan` and the bases on it, and `indi`, do
not read this directory. Each one holds its own copy of the Debian
step in its `closure.sh`, so a change here forces no new revision of
them. A change to the Debian step belongs in all three files.
