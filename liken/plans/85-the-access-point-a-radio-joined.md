# 85. The access point a radio joined

Milestone 85. Proposed 2026-10-19. A machine on wifi reports the
network it joined and nothing about how it joined it. When the link is
bad, a person cannot tell from the cluster which access point the
radio holds, on which band, or how strong the signal is. This
milestone puts the access point in status, posts an `Event` when the
radio moves to another one, and publishes the signal as a metric.

## What happened

On 2026-10-19 the switch that powers one of the testbed's two access
points lost power. Both access points serve one SSID. When power came
back, stick-1's round trip from the laptop was 3 to 12 seconds, with
10% loss and more duplicate replies than requests, while liken-1 on
the same LAN answered in 2 ms. The likely cause is that stick-1
reassociated with the farther access point and stayed there. The
cluster could not confirm it:

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
  stick-1 staying on the farther access point. A `bgscan` with a
  signal threshold makes the radio scan and move when the signal is
  weak. Decide whether `liken` sets one by default, and what it costs
  in scans on a radio that does not move.
- **A printer column.** Decide whether `kubectl get machines` shows
  the band or the BSSID, or whether the status field and the metric
  are enough.

## Done when

On stick-1, a power cycle of one access point shows the roam as an
`Event`, the new BSSID in `status`, and the signal of each access
point in `liken_wireless_signal_dbm` across the move. The manual's
wireless page names the fields, the `Event` reasons, and the metrics.
