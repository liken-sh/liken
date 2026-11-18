# The dark panel test does not reach the check it names

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`a_dark_panel_arms_no_second_off_timer` in
`src/screen/tests/panel.rs` passes today, and the code that it is
named for may be correct. The test lets the fade timer and then the
off timer expire, so the screen is asleep and the panel request is
`off`. Then it delivers a focus message with the payload `cinema` and
checks that `next_deadline()` is `None`.

`cinema` names another `Player`. `Screen::on_focus` returns before it
calls `rearm` when the message does not name this `Player`, so no
deadline could be armed whatever `rearm` does. The check that the
name describes is in `Screen::rearm`: on a sleeping screen, it arms
no off timer when the panel request is already `off`. The test never
calls `rearm` after the off timer expires, so a change that removed
that check would pass it.

A focus message that names this `Player` does not test the check
either, because `on_focus` wakes the screen first and `rearm` then
arms the fade timer. The press of the cycle key reaches `rearm` on a
sleeping screen without waking it (`Screen::on_press`), so it is a
path that the check guards.

## What would confirm it

Delete the `self.panel_request == Some(panel::OFF)` condition in
`Screen::rearm` and run `cargo test`. If
`a_dark_panel_arms_no_second_off_timer` still passes, the test does
not cover the check. The line coverage gate of `make test` can show
whether any other test reaches that condition.

## What a fix has to decide

Which input the test delivers after the off timer expires. A press of
`keys::CYCLE` while the `Player` is idle keeps the screen asleep and
calls `rearm`, so the test would then check that no second off timer
is armed. The test also has to keep or drop the focus message for
another `Player`, which tests only the early return of `on_focus`.
