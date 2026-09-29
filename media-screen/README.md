# media-screen

media-screen is the bus half of a screen client for one Player: the
timers, the focus gate, the shade, the volume step and the owner mark,
the cycle request, and the panel desire. A screen client holds these
rules in its own process. Two clients link this crate by path, so they
hold the same rules:

- media-operator's idle screen, in `media-operator/idle/`
- library-operator's media browser, in `library-operator/media-browser/`

The crate depends on the broker client and JSON, and on nothing else.
A client draws with whatever toolkit it chose, so the crate names no
window library.

`make test` runs the format check, the lints, and the tests under the
coverage gate.
