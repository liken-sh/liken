# Tests that wait on the wall clock or open real sockets

Open bug, recorded on 2026-11-18. Checked against the code on
2026-11-19: the waits are at `src/reader/alarm/tests.rs:12`, and the
fake broker binds a `TcpListener` at `src/reader/tests.rs:555`. The
poll with no limit is at `src/reader/tests.rs:446`. No unit test can
fail on a test that waits. How often they fail on a loaded runner is
not measured.

The tests of the reader and of its alarm run real threads and wait on
real time, and some of them talk to a fake broker on a real loopback
socket. The rules under "Tests run on the Go toolchain alone" in the
root `AGENTS.md` say that a test waits on a fake clock, never on real
time. The rules name Go's `synctest`, and the crate is Rust, but the
reason is the same: a test that waits on the wall clock is slow, and
it can fail on a busy CI runner. Nobody has measured how often these
tests fail.

- `src/reader/alarm/tests.rs` starts each `Alarm::wait` on a new
  thread and reads its result with `recv_timeout` and a limit of
  500 ms. `a_wait_with_no_deadline_blocks_until_a_signal` waits the
  full 500 ms on every run to show that the wait has not returned.
- `src/reader/tests.rs`:
  - `fake_broker` binds a `TcpListener` on `127.0.0.1:0`, sets a read
    timeout of 100 ms, and gives up after 5 s of wall time.
    `a_session_subscribes_to_the_clients_own_topics` and
    `a_clients_own_publish_goes_out_on_the_readers_connection` use it.
  - `a_timer_armed_by_another_thread_ends_the_clocks_wait` sleeps
    50 ms, arms a timer 50 ms out, and fails when the timer takes more
    than 500 ms to fire. `time_until_disarmed` polls the screen every
    5 ms.
  - `a_clock_with_nothing_armed_ends_when_the_client_drops_its_reader`
    sleeps 50 ms and waits up to 2 s.
  - `the_clock_fires_a_timer_and_ends_when_the_client_drops_its_reader`
    polls `reader.drain()` every 10 ms with no limit. When the clock
    never fires the timer, the test hangs and does not fail.

## What would confirm it

Each break is in the test's code. To measure the cost, run
`cargo test -- --test-threads=1` and then the default parallel run in
`media-screen/` on a loaded machine many times, and count the failures
and the time of each test.

## What a fix has to decide

How to fake time in the crate. `Alarm::wait` and the clock thread read
`Instant::now()` and block on a `Condvar` with a real timeout, so a
fake clock needs a seam, such as a time source that the `Reader` and
the `Alarm` take. The fake broker needs a transport that stays in the
process, or the tests that check the bytes on the wire move to a
connection that rumqttc can run over in memory. Until then, the poll
loop in `the_clock_fires_a_timer_and_ends_when_the_client_drops_its_reader`
needs a limit, as `time_until_disarmed` has.
