# The idle screen polls for the compositor socket

Open bug, recorded on 2026-11-18. On 2026-11-19 the code confirmed it:
`idle/src/harness/respawn.rs:187` loops on `UnixStream::connect`, and
`respawn.rs:191` sleeps between attempts. No unit test can show the
wait without real time. A drill on the `lab` fleet that restarts the
compositor would show how long the client waits after the socket
appears.

When the compositor restarts, the first process of the idle screen's
container, in `idle/src/harness/respawn.rs`, waits for the new
compositor before it starts the client again. `await_compositor`
connects to the socket, sleeps, and tries again, from `FIRST_PAUSE`
(100 ms) doubling to `LONGEST_PAUSE` (2 s), for up to
`COMPOSITOR_RETURN_LIMIT` (6 minutes). The comment says "a down
compositor sends no event that a client can wait on".

The root `AGENTS.md` names this case: a new compositor binds a new
socket file at the same path, and an inotify watch on the socket's
directory, opened before one look at the directory, gives an event
for it. The rule says that a timer that reads state again to find a
change is a defect.

## Evidence

`respawn.rs`, `await_compositor`: a `while UnixStream::connect(socket)
.is_err()` loop with `std::thread::sleep(pause)`.

## What would confirm it

This is a break of a stated rule, and the code shows it. A drill that
restarts the compositor shows how long the client waits after the
socket appears, which is up to 2 seconds.

## What a fix has to decide

Whether to watch the socket's directory with inotify and connect once
after each create event, and whether a connect that fails after the
event, while the compositor is not yet listening, needs a short retry.
The test `the_wait_ends_when_the_socket_accepts` changes with it.
