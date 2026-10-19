# 86. A radio survives a kick

Milestone 86. Proposed 2026-10-19. Partly built 2026-10-19: step 3
and the late join are built. The capture of step 1 ran on stick-1 the
same day and found no fault in the supplicant (see "The capture"), so
step 2 has nothing to fix; the lock flow is still open. When an access point's controller
disconnects stick-1 on purpose, the radio does not join again. It
fails authentication again and again until the machine reboots. An
access point controller kicks a client for ordinary reasons, such as
moving it to a better access point, so a machine on wifi must rejoin
after a kick. This milestone finds why the rejoin fails and fixes it.
It also builds the supplicant features that let a controller ask the
radio to move without a kick.

## What happens

The testbed's access points are managed by a controller with a
"reconnect" action for each client. The action sends a
deauthentication to the client. On 2026-10-19 the action on stick-1
left it failing authentication for as long as anyone watched. The
failure is easy to repeat, so it can be studied on the testbed by
hand.

The cause is not known. Three explanations fit what was seen, and
each one must be tested before the next one is considered:

- **A cached PMKSA.** After a SAE join, the supplicant keeps the
  pairwise master key and tries it first on the next join. An access
  point that dropped its copy at the kick refuses the cached key. The
  supplicant should drop its copy after the refusal, and a fault in
  that path would repeat the refusal.
- **`init`'s handling of `WRONG_KEY`.** A refusal of the cached key
  can arrive as `CTRL-EVENT-SSID-TEMP-DISABLED reason=WRONG_KEY`.
  `init` treats `WRONG_KEY` as final, and the supplicant disables the
  network for longer after each failure.
- **Protected management frames.** `ieee80211w=1` makes the
  deauthentication protected on a WPA3 association. An access point
  that sends the kick unprotected, or that expects an SA query the
  radio does not answer, can leave the two sides with different
  state.

## The change

1. **Find the cause.** Run the controller's reconnect action on
   stick-1 with the supplicant's debug output on the console, and
   record each event from the kick to the failed joins. Record the
   controller's own log for the same minutes. Write the cause here
   before any fix. The supplicant raises its log level at run time
   when it receives `LOG_LEVEL DEBUG` on its control socket, so the
   capture needs no reboot, and a radio already in the failure can be
   captured as it is. A node debug pod runs in the host's network
   namespace and mounts the host's root, so a client in that pod
   reaches the socket at
   `/host/run/liken/wireless/<if>/ctrl/<if>` and binds an abstract
   address for the reply, the way `machine.AskSupplicant` does.
2. **Fix it, with a test.** The fix depends on the cause. A test
   replays the recorded events against the stand-in supplicant
   (`machine/supplicanttest`), and fails before the fix.
3. **Let the controller steer the radio.** Build the supplicant with
   `CONFIG_WNM` for 802.11v BSS transition management, so an access
   point can ask the radio to move instead of kicking it. Consider
   `CONFIG_IEEE80211R` and the `FT-PSK` and `FT-SAE` key managements
   for fast transition between access points, and 802.11k neighbor
   reports. Each one is a change to `wpa-supplicant/build.config` and
   to the configuration in `init/wpaconfig.go`. Measure on stick-1
   whether each one changes what the controller does, before keeping
   it.

## The answers

- **A background radio that joins late gets no address.**
  `bringUpRadio` addresses a radio only when its first join succeeds.
  A background radio whose first join failed, and whose supplicant
  joins later, is associated with no address. Since milestone 85, the
  radio watch reports that radio as `Connected`, and `WirelessJoined`
  is `True` while the interface has no address. Answer: the watch
  starts the addressing on each new association of a radio with no
  address, the same addressing the park starts for a foreground
  radio. A failed addressing leaves the radio `Connected` with the
  error as its message, which is the boot's verdict for a radio that
  joined and failed its DHCP, and the next association tries again.

## What was built (2026-10-19)

Step 3 and the answer above are built. Steps 1 and 2 wait for the
capture on stick-1.

- **802.11v and 802.11k were already in the binary.** Upstream's
  Makefile turns on `CONFIG_WNM` when `CONFIG_BGSCAN_SIMPLE` is set,
  so the build of milestone 85 already answered BSS transition
  requests. Upstream builds the radio measurement code, which answers
  neighbor and beacon report requests, unless `CONFIG_NO_RRM` is set.
  `wpa-supplicant/build.config` now names `CONFIG_WNM` so the list
  shows it.
- **802.11r.** `CONFIG_IEEE80211R` is on, and `init/wpaconfig.go`
  adds `FT-PSK` and `FT-SAE` to `key_mgmt`. The rebuilt supplicant
  parses the line as the key management mask `0xd42`, which holds
  all five. The supplicant prefers the FT form only when an access
  point advertises it, so a network without 802.11r is not changed.
  The drill must check whether the controller's 802.11r setting is on,
  and whether the radio's joins use FT after it is.
- **The late join.** `init/radiowatch.go` addresses a radio on a new
  association with no address, at the watch's baseline and on each
  `CTRL-EVENT-CONNECTED`, and rewrites `resolv.conf` with the radio's
  nameservers.

## The capture (2026-10-19)

stick-1 ran release `2026.10.19-002` on a network of two access points
with one controller. The network offered WPA2 and WPA3 together, with
no 802.11r, so each join used SAE. A node debug pod attached to the
supplicant's control socket as a monitor at the debug level
(`ATTACH`, then `LEVEL 2`) and printed every message. The
controller's "reconnect" action ran three times.

- **A reconnect with no lock rejoins in 9 seconds.** The controller
  deauthenticated the radio from access point A with reason 1. Access
  point A did not answer the next authentication for 5 seconds, and
  access point B accepted the one after it. The radio watch posted
  `Joining` and then `Roamed`, 7 seconds apart.
- **A reconnect with the radio locked to one access point is out for
  minutes.** The controller's "lock to access point" setting refuses
  the client everywhere but the locked one. After a reconnect from the
  locked access point, every access point refused each authentication
  with status 37 ("declined") about 14 ms after the first SAE frame,
  so no key was checked. The supplicant backed off 10, 20, 30, and 60
  seconds, and the radio joined the locked access point 2 minutes 15
  seconds after the kick. The time the lock was lifted in the third
  run is not recorded, so the capture does not separate the end of
  the refusal from the lift.
- **The supplicant is not at fault.** It flushed its keys at each
  disconnect, tried every access point it could see, and joined on
  the first attempt that an access point accepted. Its backoff caps at
  300 seconds, so it never stops trying. The cached PMKSA and the
  `WRONG_KEY` explanations do not fit: no attempt reached the key.
- **The controller sent no 802.11v request.** It moved the client by
  deauthentication only. It sent two 802.11k radio measurement frames.
- **The refusals reach the cluster through init.** init sends a
  child process's output to the serial console and not to kmsg
  (`init/console.go`), so the supplicant's own lines stay on the
  console. The radio watch prints a line of its own for each refusal
  and each wait, and those lines reached the cluster's logs for all
  three kicks. They printed the raw event, with the status code as a
  number.

The lock flow is still open: a person who locks the radio to an
access point and then runs the reconnect action gets an outage of
minutes. Whether that comes from the controller alone, and whether a
lock set while the radio is on another access point moves it without
an outage, is not measured yet.

## What was built after the capture (2026-10-19)

- **The refusals print in words.** The console line for an
  authentication or an association refusal names the access point
  and the 802.11 status code with its meaning, from a table in
  `machine/supplicant.go` beside the disconnect reasons. The line for
  `CTRL-EVENT-NETWORK-NOT-FOUND` says that no access point is left to
  try, which is also true when the supplicant ignores each access
  point that refused it.
- **The wait after a refusal is capped at 30 seconds.** When the
  supplicant disables the network for more than 30 seconds with the
  reason `CONN_FAILED`, the radio watch sends `SELECT_NETWORK` after
  30 seconds. That clears the wait and the count of failures and
  starts a join. A wrong passphrase keeps the supplicant's whole wait.
  In the third run, the last refusal came 1 minute 14 seconds after
  the kick and the join 2 minutes 15 seconds after it, so the refusal
  ended somewhere in the 60-second wait between them. With the cap,
  the radio would have tried again at 1 minute 44 seconds.

## Done when

On stick-1, the controller's reconnect action leaves the radio joined
again within a minute, three times in a row, with an `Associated` or
`Roamed` `Event` for each rejoin. The controller's steering moves the
radio between access points without a deauthentication, or this plan
records why it cannot.
