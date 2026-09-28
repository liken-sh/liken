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

Two bounds are missing, and they are different:

- The prepare does not record earlier failures. A mode that failed
  to sync fails again on the next retry within seconds. Nothing
  backs the attempt off or fails the claim permanently, so a panel
  that needs a quiet period to recover never gets one.
- The teardown races the retries. A claim whose consumer is gone
  can still be preparing. Each attempt restarts the compositor, and
  each restart interrupts the clients that are already drawing on
  the card.

The fix will likely combine a retry budget or backoff on the mode
prepare with a check, before an expensive attempt, that the claim's
consumer still exists. The fix must not fail a claim for a panel
that syncs slowly but does sync. So the 10-second readback window
and the retry policy have to be designed together, against panels
measured on real hardware.
