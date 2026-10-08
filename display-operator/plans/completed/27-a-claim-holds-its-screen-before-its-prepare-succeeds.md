# A claim holds its screen before its prepare succeeds

Plan 27. Built and drilled on stick-1 on 2026-10-08.

A claim that stated a mode could start a loop of compositor restarts
that only the kubelet's crash backoff slowed down. The open problem
"A stuck mode prepare restarts the compositor without bound" recorded
the loop on 2026-08-27 and read it as a panel that refused to sync a
mode. A drill on 2026-10-08 reproduced the loop with a panel that
synced the mode, and found that the operator fought itself.

## The drill that reproduced it

The drill ran on stick-1 of the liken-1 testbed, on release
`2026.10.08-001`, with the `lab-portable` `Player` on the
`boe-1080-display` panel. That `Display` rests at `1920x1080@60`. The
`Player`'s display parameters stated `mode: 1280x720@60`, and a `Play`
of `pattern://bars/1280x720` started on it.

The first `Play` ran: one restart, and the panel showed 720p. Its
delete restored 1080p with a second restart. That restart came within
a minute of the first, so the kubelet held `weston` in its crash
backoff for 15 s, and the `Display` pass's 10 s readback recorded a
decline of 1080p that was not one.

A second `Play` at 720p then started the loop:

| Time | What happened |
|---|---|
| 17:44:56 | The claim's prepare writes 720p to the record and ends `weston`. |
| 17:45:06 | `weston` is still in its crash backoff. The readback finds no mode after 10 s and fails the prepare with `errModeDeclined`. |
| 17:45:20 | `weston` starts and serves 720p. |
| 17:45:21 | The `Display` pass finds the screen at 720p and its resting mode at 1080p. No CDI spec names the claim, because its prepare failed, so the pass reads the screen as free. It writes 1080p to the record and ends `weston`. |
| 17:47:25 | The kubelet retries the prepare. The record now names 1080p, so the restart budget does not match, and the prepare ends `weston` again. |
| 17:48:48 | The `Display` pass ends `weston` for 1080p again. |

The backoff grew from 23 s to 48 s to about 80 s. Every claim on the
card waited while `weston` was down, the idle screen's draw claim
included. The drill deleted the `Play` at 17:48, and the screen came
back at 17:51 when the last backoff ended.

## The faults

**The `Display` pass did not see a claim whose prepare had not
finished.** `preparedOutputs` reads the CDI specs on disk, and the
prepare writes a spec only when it succeeds. The restart budget in
`applyMode` compares the record with the last restart, and the
`Display` pass rewrote the record between two retries, so the budget
never matched.

**The readback read the crash backoff as a decline.** The kubelet
counts each restart the operator orders as a crash, so a second
restart within ten minutes waits 10 s, then 20 s, up to 5 minutes. The
10 s readback started at the restart, so any wait in the backoff
failed the switch as a decline, and the panel looked as if it refused
the mode.

**The compositor's absence evicted the pod that asked for the
restart.** A slice pass that finds no compositor serving taints every
output and draw device with `display.liken.sh/disconnected`, so the
taint manager evicts the clients that lost their connection. The pod
whose prepare ordered the restart was evicted with them, about a
second after the restart, although it had started nothing. Its
deletion freed the screen, and the media operator's new pod switched
the mode again. Drill 3 found this fault, below.

## What changed

The `Display` pass asks the API server before it restarts the
compositor, for a resting mode or for a heal (`screenholds.go`). A
claim holds a screen when its allocation names the screen's output
device in this node's pool, and a pod that the API server is not
deleting holds the claim. The pass reads the specs on disk first,
because that read costs no request, and lists the claims only when a
restart would follow, at most once per pass. A pass that cannot list
the claims restarts nothing.

The answer comes from the API server and not from memory that the
prepare fills. A record in memory would need the kubelet to call
unprepare for a claim whose prepare never succeeded, and it would be
empty after the operator's container restarts.

The readback has two parts. `awaitCompositor` waits for a connection
newer than the one the restart ended, up to `compositorReturnLimit`,
six minutes, which is past the backoff's 5-minute cap. Then the 10 s
of `modeSwitchTimeout` start. Only the second part declines. A
compositor that does not start by the limit, or a prepare whose call
the kubelet ends first, fails without a decline. The retry then finds
the mode in place, or fails without a restart while the compositor is
still down.

A switch holds the lock of the mode switches for the whole wait, so a
`Display` pass that waits for a compositor in backoff holds up the
prepares on the card, and the passes over the card's other panels,
for as long as the backoff lasts. Nothing on the card can draw until
the compositor starts, so the wait costs no picture.

A slice pass that finds no compositor serving leaves the taint off an
output whose claim is still preparing: the API server names a live
claim on the output, and no spec on disk names it. The draw devices
and every prepared output still take the taint, because their clients
did lose their connection. The slice pass lists the claims only while
no compositor serves, and it taints every output when the listing
fails.

The deletion half of the open problem needs nothing more. A `Play`'s
delete marks its pod for deletion, and the `Display` pass then reads
the claim as free. The kubelet stops the prepare's retries when the
pod goes.

## Drill 3

Drill 3 ran on stick-1 on 2026-10-08 with
`2026.10.08-002-dev-004-00461346`, which held the first two changes
and not the third. A `Play` at 720p ran, its delete restored 1080p,
and a second `Play` at 720p started at once. The second `Play` never
ran. Each restart tainted the screen, the taint manager evicted the
waiting playback pod within two seconds, and the `Display` pass then
read the screen as free, as the new check says it should for a pod
that is being deleted. After the second `Play` started, the screen
switched between the two modes six times in 11 minutes, from 18:23:35
to 18:34:21, and the last wait in the backoff was at its 5-minute cap. The readback waited out each backoff and recorded no false
decline.

## Drill 4

Drill 4 ran the steps of drill 3 on stick-1 on 2026-10-08 with
`2026.10.08-002-dev-005-62f23f63`, which holds all three changes:

| Time | What happened |
|---|---|
| 18:53:20 | The first `Play`'s prepare switches to 720p. The compositor is back in 4 s. |
| 18:53:31 | The `Play` is deleted, and the `Display` pass restores 1080p. |
| 18:53:32 | The second `Play`'s prepare finds no compositor serving and fails, as it should. Only the draw device takes the taint, and the playback pod stays. |
| 18:55:00 | The kubelet's retry switches to 720p. The compositor waits 28 s in its backoff, and the prepare waits with it. |
| 18:55:30 | The second `Play` runs at 720p. |

The compositor restarted three times, once for each change of mode
that a person asked for. No restart was repeated, the `Display`
recorded no decline, and the screen never switched back while the
claim waited. The second `Play` took two minutes to start, because
the kubelet's retry of a failed prepare waited 76 s and the
compositor's backoff 28 s more. The delete at 19:03 restored 1080p
with one more restart, which waited 58 s in the backoff.

## What stays open

The restart budget, `restarted` in `draPlugin`, is still in the
operator's memory. A restart of the operator's container clears it, so
one real decline can cost one more compositor restart after an
operator restart.

The crash backoff still darkens every screen on the card for its whole
wait. [Plan 29](29-the-compositor-restarts-inside-its-container.md)
holds the options.

The panel in the drill answered some DDC/CI reads with the reply to
another VCP code, and the first prepare failed on its brightness read
before it reached the mode. That is its own fault.
