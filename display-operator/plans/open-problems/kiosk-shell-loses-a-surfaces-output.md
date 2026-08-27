# kiosk-shell loses a surface's output

When an output is destroyed, Weston's kiosk-shell orphans every
shell surface on it: `kiosk_shell_surface_notify_output_destroy()`
sets the surface's output to NULL and nothing more. Core reflow
then parks the orphaned view on a surviving output by geometry,
without a new configure, so a fullscreen client keeps the size of
the output that died. When the output returns, nothing re-matches
the surface to it: the app-id assignment in
`desktop_surface_committed()` is one-shot behind
`appid_output_assigned`, which is never cleared.

The result on the lab machine (2026-08-27): an ultrawide's input
switch destroyed and re-created its output, and the portable
panel's idle client kept a 3840x1600 canvas, cropped on a
1920x1080 screen, while the kernel drove every connector's correct
mode.

The behavior is unchanged from the 14.0.2 this operator ships
through 16.0.0, and kiosk-shell reads no configuration that could
pin a surface across its output's destruction. Related upstream
material:

- [Issue 731](https://gitlab.freedesktop.org/wayland/weston/-/issues/731),
  the same symptom class, fixed for desktop-shell only
  (MR 1197, `a991691e`); kiosk-shell got no equivalent.
- Commit
  [`daef6793`](https://gitlab.freedesktop.org/wayland/weston/-/commit/daef679330fafeb4b5387078802143b1a4e95b2b)
  (2026-08-17, unreleased) documents the hazard window, where the
  DRM backend defers the output's destruction across a pending
  flip, and only guards the NULL crash: "a commit/activate that
  races with a pending output reassignment is simply deferred".
- Open issues in the same family:
  [496](https://gitlab.freedesktop.org/wayland/weston/-/issues/496),
  [498](https://gitlab.freedesktop.org/wayland/weston/-/issues/498),
  [1023](https://gitlab.freedesktop.org/wayland/weston/-/issues/1023).

[Plan 10](../10-the-compositor-heals-the-canvas.md) works around
the defect with a compositor restart when the screens are free. The
answer this problem waits for is upstream: kiosk-shell clearing
`appid_output_assigned` when a surface's output is destroyed, and
re-running its output matching when a matching output is created,
so the surface returns home with a correct configure. That is a
contribution to Weston, not a carry, and this project patches no
dependency.
