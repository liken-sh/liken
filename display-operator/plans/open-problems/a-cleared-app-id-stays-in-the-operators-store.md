# A cleared app_id stays in the operator's store

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

The layout module reads each surface's xdg `app_id` at every commit
and sends a `surface-app-id` line to display-operator when it changes.
The operator records the value in its store, reports it in
`status.surfaces[].appID`, and counts surfaces by it in `arrangers.go`.
When a client sets its `app_id` back to the empty string,
`read_app_id` in `weston/layout/liken-layout.c` stores the empty value
and calls `send_app_id`, and `send_app_id` returns without a line when
the value is empty. So the operator keeps the old `app_id` for that
surface until the surface goes.

The subtest "an empty app_id clears it" in `arrangerstore_test.go`
feeds the store a bare `surface-app-id 4` line, which the module never
sends.

## Evidence

- `liken-layout.c`, `send_app_id`: `if (!s->app_id[0]) return;`.
- `liken-layout.c`, `read_app_id`: compares, copies, then calls
  `send_app_id` and `arrangement_surfaces_changed`.
- `arrangerstore.go`, `applyAppID` sets `surface.AppID` from the line,
  and handles an empty value only if a line arrives.

## What would confirm it

A Wayland client on the lab fleet that sets an `app_id`, then sets an
empty one, while someone reads `status.surfaces` of its `Display`.

## What a fix has to decide

Whether the module sends `surface-app-id <id>` with no value for an
empty `app_id` after a non-empty one, which the store already parses.
`send_state`, which replays the surfaces to a new connection, also
calls `send_app_id`, and an empty value there needs no line.
