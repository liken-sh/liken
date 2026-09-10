# 54, The browser returns on the status edge

Built, and drilled on `liken-1` on 2026-09-10 in release 2026.09.10-002,
which rolled to the house the same evening. The browser maps no fresh
window when a `Play` ends and passes no app-id to the compositor.
display-operator's
[plan 17](https://github.com/liken-sh/display-operator/blob/main/plans/completed/17-a-layout-for-every-screen.md)
moved the compositor to ivi-shell, under which the browser's window is
visible again the moment the film's surface over it goes. The
media-operator half is
[media-operator plan 29](https://github.com/liken-sh/media-operator/blob/main/plans/completed/29-the-compositor-shows-what-is-under-a-film.md).

## The problem

Under kiosk-shell the browser's window stayed hidden after a film, so
the browser answered the operator's `re-present` on the `Player`'s
commands topic by mapping a second window, moving its surface to it,
and playing its return animation. That is a second `wgpu` instance
held for the life of the program, a `surface_pending` state in the
harness, a `represent` path that creates a window before it drops the
old one, and the app-id the second window had to repeat. Under
ivi-shell the compositor shows the lower window on its own, and the
operator stops publishing the re-present.

## The design

The browser's cue becomes the status edge it already reads. Today
`Moment::Status` covers the browser while the activity is `Playing`
and `Moment::Present` uncovers it, marks it returning, and lifts the
shade. The move of the activity to `Idle`, from `Playing` or from any other
activity, does what `Present` did, less the window:
`refresh.cover(false)`, `returning = true`, and `lifted()`. A `Play`
that never played returns the page on that same edge.
`Moment::Present`, `surface_due`, `surface_pending`, `represent`, and
the held `wgpu::Instance` go from the harness, and the browser reads
nothing off the commands topic. `CommandsTopic` stays in the status
the operator publishes, because `play-next` still travels there for
the client that starts the next `Play`.

The `app_id` option and the `DISPLAY_APP_ID` read go. `winit` names
the window itself, and the compositor reads the claim's socket, not
the name.

This plan requires display-operator plan 17 on the cluster before it
rolls there. Under kiosk-shell a browser that maps no fresh window
stays hidden after a film. Both clusters run plan 17, and
display-operator rolls first anywhere else.

## How the work is proved

Built in 81d6ea2 and drilled on `liken-1` on 2026-09-10 from
development build 2026.09.10-001-dev-001, after display-operator and
media-operator 2026.09.10-001. A film started on the portable panel's
`Player` and then deleted left the browser as the surface on the
screen, with no restart of the browser. The drill widened the edge: the
return fires on the status's move from any activity to `Idle`, not on
the move from `Playing` alone, so a `Play` that never played returns
the page too. Measured through the browser's headless harness, the
browser draws its first frame within about 60 ms of the `Idle` status
arriving.

The `media-screen` pin names the commit that carries media-operator
plan 29, and not a release tag, because the compositor half and this
half are one change.

The plan as written:

1. `make test` in the operator and the browser.
2. A development build rolls to `liken-1`. A film starts from the
   browser and ends, and the browser is on the screen with its return
   animation within a frame of the film's surface going.
3. The browser's environment carries no `DISPLAY_APP_ID` read, and it
   lands on its screen.
