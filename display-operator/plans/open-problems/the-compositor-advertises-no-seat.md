# The compositor advertises no seat

Open problem. The headless weston of the `weston` image advertises no
`wl_seat`, and a GTK 3 program that opens a modal dialog with no seat
crashes. PHD2, the guider in [root plan 74](../../../plans/74-astrophotography.md),
crashed this way twice on 2026-10-04. The display-operator compositor
on a machine with no input devices probably has the same gap. That
was not checked.

## The evidence

The test ran `ghcr.io/liken-sh/weston:20260928-1` in Docker on a
workstation, with the arguments and `weston.ini` of
`weston/smoke/weston.sh`: `--backend=headless`, the ivi-shell,
`liken-layout.so`, the GL renderer, and `require-input=false`. PHD2
2.6.14 from `ppa:pch/phd2` ran in a second container on Ubuntu 26.04,
as a Wayland client through a shared runtime directory, with
`GDK_BACKEND=wayland`.

- `wayland-info` lists `xdg_wm_base`, `wl_compositor`, `wl_shm`,
  `wl_output`, and `weston_capture_v1`, and no `wl_seat`.
- weston 14.0.1 declares `struct weston_seat fake_seat` in
  `libweston/backend-headless/headless.c` and never initializes it.
  The frontend parses a `--no-input` option for the headless backend,
  and `headless.c` never reads it.
- At startup GTK logs `gdk_seat_get_keyboard: assertion 'GDK_IS_SEAT
  (seat)' failed` and `gdk_seat_get_pointer: assertion 'GDK_IS_SEAT
  (seat)' failed`. PHD2 keeps running through those.
- PHD2 crashed with `SIGSEGV` twice, each time in a modal dialog. The
  backtrace is the same in both: `wxDialog::ShowModal()` or
  `wxMessageDialog::ShowModal()`, then
  `wxWindow::GTKReleaseMouseAndNotify()`, then a fault in
  `libgdk-3.so.0`. The first dialog was PHD2's "already running"
  message. The second was its first-light profile wizard.
- With both startup dialogs turned off, PHD2 ran on the same weston,
  calibrated, and guided for 2 minutes with no crash.
- PHD2 opened the same wizard under `cage` 0.2.1 with the headless
  wlroots backend. `cage` advertises a seat with no input devices, and
  PHD2 kept running and guided with the dialog open.

So a program built on wxGTK runs on this compositor only while it opens
no modal dialog. PHD2 reports some errors in a message box, and which
errors those are was not checked.

## What is not known

display-operator runs the same image with the DRM backend, also with
`require-input=false`, because a machine with monitors has no keyboard
and no mouse. weston's libinput backend creates a seat when it finds an
input device. Whether it advertises a seat with no device at all was
not checked. A `liken` machine runs no udevd, which may change the
answer.

## Possible remedies

**`liken-layout` creates a seat.** The module is `liken`'s own code and
loads into weston. It can call `weston_seat_init` and give the seat a
keyboard and a pointer with no device behind them. Every GTK client
then finds a seat, on the headless backend and on DRM. The same seat
is also a path for a `Remote`'s presses to reach a program on a screen
as key events, which the input work in root plan 74 names.

**The client's pod restarts the program after a crash.** This needs no
change to the compositor. A guider that crashes during an exposure
ruins that exposure, and every GTK program has to be set up again
after each crash.

The first remedy changes the compositor that every screen on `liken`
uses, so it needs a design decision.
