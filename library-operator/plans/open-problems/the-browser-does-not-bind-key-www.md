# The browser does not bind `KEY_WWW`

Open problem. media-operator treats two key names as home: `KEY_HOMEPAGE`
and `KEY_WWW` (`homeKeys` in `media-operator/ensure.go`, and
`playbackKeys` in `media-operator/keybindings.go`). The kernel's
`rc-cec` keymap sends `KEY_WWW` for a TV remote's Internet button, and
some remotes send it for their home button. The browser binds only
`KEY_HOMEPAGE` (`key_of` in `media-browser/src/browser/keys.rs`). So a
press of a home button that sends `KEY_WWW` ends a film and asks the
receiver for the unit's input, and the same press in the browser does
nothing. Root
[plan 72](../../../plans/completed/72-power-and-home-during-a-play.md)
set this gap aside. The fix is one more arm in `key_of` and one more
row in the test table beside it.
