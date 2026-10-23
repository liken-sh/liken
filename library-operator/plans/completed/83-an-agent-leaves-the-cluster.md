# 83, An agent leaves the cluster

This plan, written on 2026-10-23, makes a Corrosion agent tell its
peers that it leaves when its pod stops, and wait until each peer
confirms. The peers then stop dialing the pod as soon as it goes. The
change is in `liken`'s fork of Corrosion,
[liken-sh/corrosion](https://github.com/liken-sh/corrosion), and in
the agent configuration in `corrosion/`. Built on 2026-10-23, in the
fork's release `2026.10.23-001`, which `corrosion/Dockerfile` pins.
Drilled with agents on one workstation and on `liken-1` the same
day.

## The problem

Every scan `Job`, enrichment `Job`, and cleanup `Job` runs a catalog
agent, so a busy namespace has agents that join the catalog cluster
and leave it every hour. Corrosion runs SWIM through the `foca` crate.
When an agent stops, it calls `leave_cluster`, which gossips a down
update about itself. Its peers that receive the update drop the agent
at once. Its peers that do not receive it keep probing the address
until suspicion declares the agent down, at least 30 seconds later.
Each probe and each sync to a pod that is gone is a QUIC connect
attempt that ends in a five-second timeout and an error line.

On a home cluster, one catalog agent logged 297
`could not write datagram ... deadline has elapsed` lines in six
hours. For one scan `Job` that it saw leave, the first failed sync to
the `Job`'s address came 14 seconds after the `Job`'s agent joined.
The peer declared the agent down 29 seconds after that, so no leave
had reached it. It dialed the address six more times in the next four
minutes, the last three of them one to two minutes apart. `foca`
announces to members that are down every 125 seconds until
`remove_down_after_secs`, 300 seconds then, passes, and the drill
below shows those announcements at that retention.

The physical testbed `liken-1` showed the same on 2026-10-23, with
library-operator `2026.10.21-001`. A `movies` walk `Job`'s agent
logged `error dispatching broadcast packet: channel closed` at its
SIGTERM, slept five seconds, and exited at 16:06:51. Its two peers,
the catalog pod's agent and a media browser's agent on `stick-1`,
declared it down by suspicion at 16:07:27, 36 seconds later. Both
logged failed syncs to the `Job`'s address. In the four minutes after
the `Job` ended, they logged 14 and 15 `could not write datagram`
lines for it, 9 and 10 of them after they held it as down.

Three faults in Corrosion lose the leave:

1. On SIGTERM, the SWIM runtime loop calls `leave_cluster` after the
   tripwire trips. The task that sends SWIM datagrams stops on the
   same tripwire, so the down update often finds a closed channel.
   The agent logs `error dispatching broadcast packet: channel
   closed` and nothing goes out.
2. `leave_cluster` gossips to `num_indirect_probes` members, three by
   default, and `foca` puts an update on at most `max_transmissions`
   messages. Corrosion computes that limit from the cluster size, so
   in a small cluster the down update is on one message only, and a
   member that no gossip reaches never hears it from the agent.
3. After the leave, the runtime loop slept for five seconds and could
   not tell whether any peer heard.

## The contract

When an agent stops, it lists the members it holds as active, raises
`num_indirect_probes` and `max_transmissions` to cover all of them,
and calls `leave_cluster`. It gossips the down update to every member
again every 250 ms. A member that already holds the agent as down
answers any message from it with `TurnUndead`, so each `TurnUndead`
is a confirmation. The agent stops when every member has confirmed,
or after five seconds, and logs
`left the cluster: <confirmed> of <members> members confirmed`.

The confirmation is a message that SWIM already has.
`notify_down_members`, which Corrosion turns on, makes a member send
`TurnUndead` to a sender that it holds as down. `foca` stops acting
on messages once it has left, so the runtime loop reads the header of
each message itself. The agent adds no HTTP endpoint and makes no
call outside the gossip protocol.

To receive the confirmations after shutdown begins, the SWIM sender,
the SWIM datagram readers, and the gossip server stay up until the
runtime loop ends. The gossip server accepts new connections until
then, and the stream handlers on those connections stop at once on
the tripped wire, so the connections carry only SWIM datagrams. When
the loop ends, the sender aborts the sends that are still connecting,
so a send to a member that is gone does not hold shutdown open.

The agent configuration lowers `remove_down_after_secs` from 300 to
30. After a confirmed leave no member gossips the agent as alive, so
a short retention loses nothing, and most members forget the agent
before their next announcement to it.

## The changes in the fork

Two commits, each with a test in `corro-agent`:

* `Send the SWIM leave announcement before the sender stops`. The
  sender runs until the runtime loop drops its channel.
  `test_leaving_member_is_down_before_suspicion` starts three agents,
  stops one, and requires the other two to drop it within five
  seconds. It fails without the change.
* `Wait for members to confirm a leave instead of sleeping five
  seconds`. The leave loop above, with `CorroSender::closed` and
  `CorroSender::is_closed` in `corro-types`.
  `test_leave_ends_when_every_member_confirms` requires the leaving
  agent's runtime loop to end within two seconds. It fails when the
  loop sleeps.

Both are universal fixes, and they join the `liken` stack by
cherry-pick. The fork's `AGENTS.md` admitted only build and
performance changes, and it now admits stability and reliability
fixes too. A universal change joins the stack whether or not it has
an upstream pull request, because upstream has acted on few of the
pull requests the fork sent.

## Set aside

* **A row in the catalog that says an agent left.** It needs every
  peer to sync the row before it acts, and it writes membership into
  the data that the cluster replicates.
* **An HTTP call to each peer, or a call to the API server.** The
  agents gossip without a coordinator, and the leave stays inside the
  gossip protocol.
* **`Announce` as the probe.** An `Announce` carries no updates, so
  it cannot deliver the down update to a member that missed it. A
  `Gossip` does both: it delivers the update, and it draws
  `TurnUndead` from a member that already has it.
* **A setting to stop `foca`'s announcements to down members.** The
  30-second retention removes almost all of them with no change to
  Corrosion.

## Proof

`corro-agent`'s suite passes under `cargo nextest`, as upstream's CI
runs it: 56 tests, five runs in a row, with the two commits on
upstream main at `b3a30e81`. After the move to `4d4582f7`, the suites
of `corro-agent` and `corro-types` pass with the two commits on
upstream main and on the `liken` stack.

A drill on one workstation started eight agents of the debug build on
loopback, sent SIGTERM to one, and watched the other seven for 45
seconds. Three trials of each build:

| | the stack before | the stack with the two commits |
|---|---|---|
| peers that dropped the agent at once | 7, 0, 0 | 7, 7, 7 |
| when the others dropped it | 40 to 45 s later, by suspicion; one peer not within the 45 s | none were left |
| SIGTERM to exit | 5.4 to 5.9 s | 0.4 to 1.0 s |
| `could not write datagram` lines at the peers | 53, 42, 1 | 0, 0, 0 |
| confirmations | none | 7 of 7, about 250 ms after SIGTERM |

With the two commits and a watch of 150 seconds, each of the seven
peers dialed the departed agent once at a retention of 300 seconds,
and none did at 30 seconds.

On `liken-1`, with library-operator
`2026.10.23-003-dev-001-998def09`, a `franchises` walk `Job`'s agent
received SIGTERM at 16:40:28.879 and logged
`left the cluster: 2 of 4 members confirmed` five seconds later. Its
two live peers, the catalog pod's agent and the media browser's agent
on `stick-1`, dropped it at 16:40:28.880 and 16:40:29.017, and logged
no `could not write datagram` line for its address. The baseline
`Job` above drew 14 and 15.

The two members that did not confirm were `Job` agents from earlier
walks, gone for half an hour or more. The new `Job`'s agent listed
them as members at its start, from the member table that Corrosion
stores in the database on the claim, and gossiped them as alive. Both
live peers took them back as members, probed them, and declared them
down by suspicion about 45 seconds later, with about five
`could not write datagram` lines a minute for each. A leave does not
reach an agent that is already gone, so this noise is outside this
plan.
