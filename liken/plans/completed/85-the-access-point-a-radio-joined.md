# 85. The access point a radio joined

Milestone 85. Proposed and built 2026-10-19. The drill below has
not run. A machine on wifi reports the
network it joined and nothing about how it joined it. When the link is
bad, a person cannot tell from the cluster which access point the
radio holds, on which band, or how strong the signal is. This
milestone puts the access point in status, posts an `Event` when the
radio moves to another one, and publishes the signal as a metric.

## What happened

On 2026-10-19 the access point nearest stick-1 lost power for a
while. stick-1 reassociated with a farther access point on the same
SSID and stayed there after the near one came back. Its round trip
from the laptop rose to 3 to 12 seconds, with 10% loss and more
duplicate replies than requests, while liken-1 on the same LAN
answered in 2 ms. Its heartbeat lease expired, so its `Machine` went
`Lost`. The access point's controller showed the association. The
cluster could not:

- `status.network.interfaces[].wireless` holds the SSID, the state,
  and a message. It has no BSSID, no frequency, and no signal.
- The `WirelessJoined` condition was `True` the whole time, because
  the radio was associated.
- The `liken-logs` relay could not serve the kernel log, because
  stick-1's kubelet timed out on the same link.
- `init` prints `wlan0 associated` on `CTRL-EVENT-CONNECTED`, and the
  line does not name the access point.

## The change

1. **The access point in status.** `init` already reads the
   supplicant's events (`init/wpactrl.go`). On each
   `CTRL-EVENT-CONNECTED` it asks the supplicant for `STATUS` and
   writes the BSSID, the frequency, and the band to the facts tree
   beside the SSID. `machine-operator` copies them into
   `WirelessStatus`. These values change only when the radio
   associates, so they belong in status and cost no write while the
   radio stays put.
2. **An `Event` for each move.** A connect with a BSSID that differs
   from the one in status is a roam. `machine-operator` posts one
   `Event` that names the old and the new access point, and one
   `Event` for each disconnect with its reason code. Both are actions
   that change no condition, so they follow root plan 78.
3. **The signal as a metric.** The signal level in dBm, and the
   transmit bitrate, change all the time, so they stay out of status.
   They follow the pattern of the clock's offset
   ([milestone 84](completed/84-the-clock-offset-is-a-bound-in-status.md)):
   `init` writes each reading to a file outside the facts tree, and a
   collector in `machine-operator` reads the file only when Prometheus
   scrapes. The gauges are `liken_wireless_signal_dbm` and
   `liken_wireless_tx_bitrate_bits_per_second`, labeled by interface
   and BSSID.

## Questions to answer

- **Where the readings come from without a timer.** The supplicant
  posts `CTRL-EVENT-SIGNAL-CHANGE` when the signal crosses a threshold
  set by `bgscan` or by the driver's signal monitor (nl80211 CQM). A
  reading on each crossing is an event, and it fits the rule in the
  root `AGENTS.md`. A `SIGNAL_POLL` on a timer re-reads state, and that
  rule calls it a defect. Measure how often the threshold events
  arrive on stick-1's `iwlmvm` radio, and whether they carry the level
  or only the direction.
- **Whether the radio should roam back.** The supplicant
  configuration in `init` sets no `bgscan`, so the radio leaves an
  access point only when the link drops. That is consistent with
  stick-1 staying on the far access point. A `bgscan` with a
  signal threshold makes the radio scan and move when the signal is
  weak. Decide whether `liken` sets one by default, and what it costs
  in scans on a radio that does not move.
- **A printer column.** Decide whether `kubectl get machines` shows
  the band or the BSSID, or whether the status field and the metric
  are enough.

## The answers

**The readings come from the supplicant at scrape time.** The
wpa_supplicant build had no bgscan, so `CTRL-EVENT-SIGNAL-CHANGE` never
fired. With bgscan, the driver's signal monitor fires only when the
signal crosses one threshold, so a file written on each event would
hold a stale level between crossings. `machine-operator` runs on the
host network, privileged, and mounts `/run/liken`, so its collector
sends `STATUS` and `SIGNAL_POLL` to each radio's control socket when
Prometheus scrapes. The driver measures the signal on the frames the
radio already receives, so a request sends nothing over the air. No
file and no timer exist for the signal, and the third step of the
change above did not happen as written.

**The radio roams back.** The build enables `CONFIG_BGSCAN_SIMPLE`,
and `init` writes `bgscan="simple:30:-70:3600"` into every network
block. The radio scans every 30 seconds while the signal is below
-70 dBm, and once an hour above it, and the supplicant moves to a
stronger access point of the same network. A radio that does not move
pays one background scan an hour.

**No printer column.** The status field, the `Event`, and the metric
are enough, and the table is already wide.

## What was built

Before this milestone, nothing read the supplicant's events after the
join, so a disconnect after boot reached neither the console nor
status. A new component in `init`, the radio watch
(`init/radiowatch.go`), reads them for the life of the boot. It
replaces the component that landed a background radio's verdict, so
one component writes the network subtree after `publishBootFacts`.
It asks `STATUS` once when it starts, again on each
`CTRL-EVENT-CONNECTED`, and again each time the event stream attaches
after a supplicant restart.

`status.network.interfaces[].wireless` gains `bssid`, `frequency`,
`band`, `associatedAt`, and `lastDisconnectReason`. The facts tree
carries them as one record, `wireless/association`, beside the `state`
and `message` files. Those two files keep their form, so an operator
and an `init` of different releases still read each other during a
rollout.

A disconnect after the join sets the state to `Associating`, with a
message that names the access point and the 802.11 reason code.
`WirelessJoined` reports that as `Joining`, which does not mark the
machine `Degraded`, because the supplicant rejoins on its own.

`machine-operator` posts `Roamed` when the BSSID changes, and
`Associated` for an association at boot or after a loss, with the
reason of the loss. A disconnect that the operator sees posts the
`Joining` transition of `WirelessJoined`. A disconnect and a rejoin
between two passes post only `Associated`, which carries the reason.

## Done when

On stick-1, a power cycle of one access point shows the roam as an
`Event`, the new BSSID in `status`, and the signal of each access
point in `liken_wireless_signal_dbm` across the move. The manual's
wireless page names the fields, the `Event` reasons, and the metrics.
