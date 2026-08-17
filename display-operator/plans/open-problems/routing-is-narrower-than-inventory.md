# Routing is narrower than inventory

Open problem. The operator publishes a device for every connector the
card has, and the compositor routes a surface only to the connectors
that had a monitor on them when the operator started. A connector that
was dark at startup publishes as a device that no claim can use, and it
stays that way until the operator restarts.

## What happens

The operator enumerates the connectors once, at startup, and writes one
`[output]` section into `weston.ini` for each connector that reports a
monitor. It publishes every connector as a device, whether or not a
monitor is on it, so a claim can wait for a screen that somebody
switched off. That is the useful half, and it works: a monitor
unplugged and replugged after startup only moves the taints on a device
the compositor already routes to.

The other half is a connector that was dark when the operator started.
It has no `[output]` section, so no app-id reaches it. A client sent
there would land on the first output instead, on top of that output's
rightful client. The device therefore publishes with
`display.liken.sh/no-output`, a `NoSchedule` taint that a consumer must
not tolerate, so a pod claiming it parks as `Unschedulable` instead of
covering another client's screen. `NodePrepareResources` refuses the
same device by name.

The pod stays parked for as long as the operator runs. The monitor is
there, its EDID publishes as attributes on the device, and the claim
still cannot start.

## The workaround

Restart the operator. It enumerates the connectors again, writes the
section, and clears the taint.

    kubectl rollout restart -n liken-system deploy/display-operator

The cost is stated in the [README](../../README.md#disconnects-and-restarts):
the new pod starts a new compositor, so every client on every screen
loses its Wayland connection, which for most clients means the pod
restarts.

## What an answer has to weigh

The question is whether the operator should reconfigure the compositor
while it runs, and what that costs.

* Weston reloads some of `weston.ini` and not all of it. Which parts,
  and whether an added `[output]` section is one of them, is not
  established here.
* A restart of the compositor ends every consumer's session, not only
  the session on the connector that changed. So an answer built on a
  restart lets one new monitor disturb every screen that has nothing to
  do with it.
* This operator's contract is that a pod is one session. CRI carries
  CDI devices at container creation only, so a running consumer's
  device set never changes, and the taint is what ends a session so the
  scheduler can start the next one. An answer that changes the routing
  under a running client has to say what that client's session is.

Minted app-ids wait on the same question, for the same reason: minting
an app-id per allocation changes the routing table while the
compositor runs. That question came here when
[milestone 57](https://github.com/liken-sh/liken/blob/main/plans/completed/57-the-display-operator.md#open-questions)
completed, having shipped the simpler half rather than decided the
choice. A fixed string per output lets a client outside the cluster
take a screen by guessing the string; a minted one is a capability and
costs a config change on every allocation. Version 0 writes one fixed
app-id per output and takes the restart.

No answer is chosen.
