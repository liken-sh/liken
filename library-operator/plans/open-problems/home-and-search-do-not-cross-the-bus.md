# Home and search do not cross the bus

Open problem, found on 2026-09-06 while the manual was written. The
browser binds home, search, backspace, space, and every letter and
digit, and plan 39 drilled them from a keyboard on the workstation.
Over the bus, a remote's presses reach the browser through the
`media-screen` crate that `media-operator` publishes, and the crate
forwards only navigation: the arrows, enter, and back. A home or
search button on a remote, and a letter from a remote's keyboard, stop
at the crate's gate and never reach the browser.

The fix is in `media-operator`: widen the crate's forwarding table,
or forward every key the crate does not handle itself and let the
delegate decide. Then this operator bumps its pin on the crate, and the
`liken-1` drill plan 39 still owes, an X6 bound by a `Keymap` row
reaching home and search, proves it. The manual's browser guide states
the limit as it stands today.
