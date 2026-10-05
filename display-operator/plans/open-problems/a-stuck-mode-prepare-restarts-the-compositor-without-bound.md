# A stuck mode prepare restarts the compositor without bound

A claim that states a mode the panel will not sync turns the
kubelet's prepare retries into a compositor restart loop. Each
prepare applies the mode, restarts the compositor, and waits 10
seconds for the connector to report it; when the panel never syncs,
the prepare fails, the kubelet retries, and the loop taints every
device on the card while the compositor restarts again and again.
After enough crashes the kubelet holds the compositor in restart
backoff for minutes, and every claim on the card stays pending, the
idle screens included.

Observed on real hardware on 2026-08-27, twice in one evening:

- The lab's portable panel accepted `1280x720@60` in the morning
  and refused to sync it at night, so a `Play` whose claim stated
  that mode repeated the prepare in a loop. The panel also stopped
  answering DDC/CI during the restart loop, and it recovered when
  the loop stopped.
- Deleting the `Play` did not end the loop at once: the claim's
  teardown lagged the pod's, and prepare retries for the dying
  claim kept restarting the compositor until the claim was deleted
  by hand.

The prepare has one bound, and the drill shows that it is not enough:

- The prepare records one earlier failure. Since b25e3ec4
  (2026-08-19), `applyMode` in `modes.go` keeps a restart budget in
  `restarted`, keyed by connector. When the config already asks for a
  mode and a restart already ran for it, the prepare fails with
  `errModeDeclined` and does not restart the compositor again. The
  release of the claim (`releaseModes`) clears the entry, so the next
  claim gets its own restart. The budget is in memory, so a restart of
  the operator clears it too. The drill on 2026-08-27 ran with this
  budget and still saw the loop, so the budget did not stop it. The
  likely remaining cause is the teardown race below, and that is not
  proven. The budget also fails each retry within seconds and never
  fails the claim permanently, so a panel that needs a quiet period
  to recover still gets no backoff.
- The teardown races the retries. A claim whose consumer is gone
  can still be preparing. Each attempt restarts the compositor, and
  each restart interrupts the clients that are already drawing on
  the card.

The fix will likely combine a backoff or a permanent failure on the
mode prepare with a check, before an expensive attempt, that the claim's
consumer still exists. The fix must not fail a claim for a panel
that syncs slowly but does sync. So the 10-second readback window
and the retry policy have to be designed together, against panels
measured on real hardware.

[The operator's restarts wait in the kubelet's crash backoff](the-operators-restarts-wait-in-crash-backoff.md)
depends on this fix. Its first option restarts weston inside the
container, which removes the kubelet's backoff, and the backoff is the
last bound on this loop.
