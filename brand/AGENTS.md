# Working on brand

This directory holds the brand domain of [`liken`](https://liken.sh/): the
mark, the stylesheet, the Hugo theme, the voice rules, and the Go and
Rust programs that the other components run to build their sites.
`README.md` explains each part and the consumers that read it.

Every other `liken` repository takes this one as a git submodule. A
change here reaches a site only when that site bumps its submodule.

`voice.md` is the voice for every word the project publishes. Read it
before you write prose here.

`make test` runs every check CI runs.
