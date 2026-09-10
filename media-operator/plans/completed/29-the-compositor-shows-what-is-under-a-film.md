# 29, The compositor shows what is under a film

Built, and drilled on `liken-1` on 2026-09-10 in release
2026.09.10-001. The re-present retires, and so does the app-id every
client passed back to the compositor. display-operator's
[plan 17](https://github.com/liken-sh/display-operator/blob/main/plans/completed/17-a-layout-for-every-screen.md)
moved the compositor from kiosk-shell to ivi-shell under a module of
its own. Under it a lower surface is visible the moment the surface
over it goes, and the socket a client arrived on is what says which
screen it belongs to. Two workarounds for kiosk-shell come out of this
operator and its clients. The library-operator half is
[library-operator plan 54](https://github.com/liken-sh/library-operator/blob/main/plans/completed/54-the-browser-returns-on-the-status-edge.md).

## The problem

Both workarounds were correct for kiosk-shell and are dead weight
under ivi-shell.

**The re-present.** kiosk-shell reveals a lower surface only along a
code path gated on a seat, and a `liken` compositor has none, so when
a film's surface went the idle clock stayed hidden and the screen went
black with the client still running. The operator publishes
`{"action": "re-present"}` on the `Player`'s commands topic at the
edge from any active state to idle, and the client maps a fresh
surface, which kiosk-shell reveals along a seat-independent path. That
is a topic, an action in the media vocabulary, an ordering rule
between the status write and the command, a second surface and a
second `wgpu` instance in each client's harness, and a section of the
delegate guide. Under ivi-shell the module owns the stacking order,
and the clock is visible the moment the film's surface goes, so the
re-present fires and changes nothing.

**The app-id.** kiosk-shell routed a window to an output by the
app-id the client set, so the playback pod passed
`--wayland-app-id=$(DISPLAY_APP_ID)` and the idle client read
`DISPLAY_APP_ID` into its window attributes. Under ivi-shell the
claim's own socket is the identity and the module ignores app-ids.
display-operator still delivers the variable, unread, and retires it
in a later plan of its own.

## The design

The operator stops publishing the re-present. `actionRePresent` and
`publishRePresent` go, and the edge in `operate.go` that published it
goes with them. The commands topic stays, because the playback pod's
`play-next` still travels on it, and `status.idle.bus.commandsTopic`
keeps its place in the contract with a description that names what is
left on it.

The idle client and the media-screen crate stop answering the command.
The client already treats the retained status's move to `Idle` as its
cue, so what remains is one cue instead of two: `Moment::Present`,
`surface_due`, `surface_pending`, and `represent` go from the idle
harness, and the `wgpu` instance the harness held for a second surface
goes with them. A client with one surface for its whole life is the
plain case every toolkit is built for.

The playback pod passes no app-id. `displayAppIDVariable` and the flag
go from `player.go`, and the idle client's `APP_ID` wiring and the
`app_id` window attribute go from `idle/`. `winit` picks its own
app-id, and nothing reads it.

The delegate guide loses its re-present paragraph and gains one
sentence: when a `Play` ends, the client's surface is visible again on
its own, and the status is the cue. The bus reference and the
`Player` CRD's description of the commands topic say the same.

### The order of the rollout

This plan requires display-operator plan 17 on a cluster before it
rolls there. Under kiosk-shell a client that passes no app-id lands on
whichever output weston enumerated first, and an idle client that maps
no fresh surface stays hidden after a film. display-operator
2026.09.10-001 rolled to the testbed and to the house first, and this
operator's 2026.09.10-001 followed the same evening.

## What was considered and set aside

- **Keep the re-present as a no-op for old compositors.** It would let
  this operator roll before display-operator anywhere. It also keeps
  the second surface path in two clients for a compositor nobody will
  run after the fleet moves. The rollout order is one line to follow.
- **Drop the commands topic.** `play-next` travels on it, so it stays.

## How the work is proved

Drilled on `liken-1` on 2026-09-10, on the `2026.09.09-002-dev-001`
build of commit `6cb1ecc`. A `Play` on the portable panel's `Player`
ran under its own claim socket, with its playback label in the
`Display`'s `status.surfaces`, and the playback argv carried no
`--wayland-app-id`. The `Play` delete left the idle surface as the one
the default region held, and the operator's log held no re-present
line.

Commit `3348eae` followed the same day, in the same release. The idle
pod carries `media.liken.sh/component: idle`, so a `Layout` names the
region the idle screen draws in, and the mark's arrival from
`Starting` reverses the ramp from where it stands instead of starting
it over. The plain idle screen was then drilled on the portable panel
under the `theater` `Layout`: the clock drew in its own region, a film
came in over it and left, and the status read `Idle` 144 ms after the
ending report.

The plan as written:

1. `make test` in the operator and in `idle/` and `media-screen/`.
2. A development build rolls to `liken-1`. A `Play` starts over the
   idle clock and ends, and the clock is on the screen within a frame
   of the film's surface going, with no re-present in the operator's
   log and none on the bus.
3. The playback pod's argv carries no `--wayland-app-id`, and the film
   lands on its screen.
