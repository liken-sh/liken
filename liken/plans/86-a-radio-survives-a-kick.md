# 86. A radio survives a kick

Milestone 86. Proposed 2026-10-19. When an access point's controller
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
   stick-1 with the supplicant's debug output on the console (`-dd`
   for one boot), and record each event from the kick to the failed
   joins. Record the controller's own log for the same minutes. Write
   the cause here before any fix.
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

## Questions to answer

- **A background radio that joins late gets no address.**
  `bringUpRadio` addresses a radio only when its first join succeeds.
  A background radio whose first join failed, and whose supplicant
  joins later, is associated with no address. Since milestone 85, the
  radio watch reports that radio as `Connected`, and `WirelessJoined`
  is `True` while the interface has no address. Decide whether the
  watch starts the addressing on such a join, as the park does for a
  foreground radio.

## Done when

On stick-1, the controller's reconnect action leaves the radio joined
again within a minute, three times in a row, with an `Associated` or
`Roamed` `Event` for each rejoin. The controller's steering moves the
radio between access points without a deauthentication, or this plan
records why it cannot.
