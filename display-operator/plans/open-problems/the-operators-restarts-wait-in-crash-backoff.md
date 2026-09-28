# The operator's restarts wait in the kubelet's crash backoff

The operator restarts the compositor by ending its process, and the
kubelet restarts every container that exits through its crash
backoff. The first restart after a quiet period starts weston again at
once. A second restart within about 10 minutes waits 10 seconds before
weston starts, a third waits 20 seconds, and the wait doubles up to 5
minutes. Every output on the card is dark for the whole wait.

## How the operator restarts the compositor

The operator ends the compositor for four reasons. Each reason has a
label on `display_compositor_restarts_total`:

- `mode`: a claim or a `Display`'s `spec.mode` needs another mode
  ([plan 05](../completed/05-choosing-the-mode.md)).
- `heal`: an output was destroyed and created again, and no claim
  holds a screen ([plan 10](../completed/10-the-compositor-heals-the-canvas.md)).
- `hung`: the compositor accepts on its socket and answers nothing
  for 10 s ([plan 21](../completed/21-the-compositor-handshake.md)).
- `masterless`: the compositor runs without DRM master
  ([plan 24](../completed/24-the-compositor-holds-drm-master.md)).

The first, second, and fourth send `SIGTERM` to weston. `hung` sends
`SIGKILL`. [Plan 04](../completed/04-the-kubelet-supervises-the-compositor.md)
made weston the one process of the `weston` container, a native
sidecar with `restartPolicy: Always`, so the end of weston is the
container's exit, and the kubelet's restart is the only supervision.
The kubelet cannot tell an exit that the operator ordered from a
crash. It puts both through the same backoff, whatever the exit code.

## The kubelet's backoff

liken-1 runs k3s `v1.36.4+k3s1`, the version in `liken`'s
`k3s/VERSION`, so its kubelet is Kubernetes 1.36.4. The numbers below
come from the Kubernetes source at the tag `v1.36.4`:

- `pkg/kubelet/kubelet.go` sets the first wait,
  `initialCrashLoopBackOff`, to 10 s, and the longest,
  `MaxCrashLoopBackOff`, to `MaxContainerBackOff`, which is 300 s.
- `NewMainKubelet` in the same file sets the reset rule: the backoff
  of a container starts over when the container exits more than 600 s
  after the kubelet's last restart of it.
- `Backoff.Next` in
  `staging/src/k8s.io/client-go/util/flowcontrol/backoff.go` starts a
  new entry at the first wait and doubles an existing entry up to the
  longest wait. The kubelet sets no jitter.
- `doBackOff` in `pkg/kubelet/kuberuntime/kuberuntime_manager.go`
  runs before every container start, a sidecar's included. It counts
  the wait from the time the container exited. A container with no
  entry starts at once, and the kubelet creates its entry then.
- `GetBackoffKey` in `pkg/kubelet/kuberuntime/helpers.go` keys the
  entry by the pod's name, namespace, and UID and the container's
  name, with the container's image and resources, so a new pod starts
  with no entry. The entries are in the
  kubelet's memory, so a kubelet restart also clears them.

So in one pod, the first exit restarts at once. Each later exit
within 600 s of the last restart waits 10, 20, 40, 80, 160, and then
300 seconds. The kubelet acts on the end of the wait at its next sync
of the pod, so the dark time is a little longer than the wait.

## What the drill measured

The drill ran on liken-1 on 2026-09-27, on the testbed machine with a
connected panel:

- The `masterless` restart in plan 24's drill waited 12 s in the
  backoff, because the container had exited twice in 15 s.
- Two mode switches in a row each took about 30 s from the switch to a
  picture on the panel.

The drill did not record how the 30 s divides into the backoff, the
kubelet's sync, weston's startup, and the operator's readback.

## What it costs

A restart that comes more than about 10 minutes after the last one
costs about a second, as plan 04 measured. A restart within those 10
minutes leaves every screen on the card dark for 10 s or more, and
each further restart doubles the wait. These sequences reach the
backoff in normal use:

- A film whose claim states a mode shorter than 10 minutes. The claim
  switches the mode, and its end restores the resting mode that
  [plan 09](../completed/09-the-display-reports-the-screen.md) adds.
- Two films in a row that state different modes.
- A heal or a `masterless` restart soon after a mode switch.

The mode switch also has a time limit that assumes a fast restart.
`modeSwitchTimeout` in `modes.go` is 10 s, and its comment counts
about a second of kubelet turnaround. A wait of 10 s or more in the
backoff is longer than that window, so the switch can fail with
`errModeDeclined` while weston waits to start. The result depends on
the caller:

- For a claim's prepare, the kubelet retries the prepare. A retry
  after weston is back finds the mode in place and delivers. A retry
  before weston is back fails with `errModeDeclined` again, because
  the mode record and the last restart already name the mode.
- For the restore of a `Display`'s resting mode, no kubelet retry
  exists. The `Display` pass records a decline in
  `status.unconfirmed`, and the entry clears only on a later pass that
  finds the mode in place.

This is read from the code and was not seen in a log. It can explain
part of the 30 s.

[A stuck mode prepare restarts the compositor without bound](a-stuck-mode-prepare-restarts-the-compositor-without-bound.md)
is the same backoff at its limit: there, repeated restarts put the
compositor in minutes of backoff. That problem is about a mode that
never syncs, and the backoff is the only bound on its loop. This
problem is about restarts that succeed.

## The options

None is chosen.

1. **Restart weston inside the container.** The container's first
   process stays up, runs weston as its child, and starts weston again
   when the operator orders a restart. The operator sends its order to
   that process, not to weston, so the kubelet sees no exit and no
   backoff applies. When weston exits without an order, the first
   process exits too, so the kubelet's backoff still bounds a crash
   loop. This depends on:
   - replacing plan 04's `exec`, which makes weston the container's
     one process;
   - plan 21's `SIGKILL` path and plan 24's pid rules, which find
     weston by the path of its executable in the pod's shared process
     namespace
     and must still see each new weston as a new process;
   - a fix for the stuck mode prepare first, because this option
     removes the only bound that loop has.
2. **A supervisor in the sidecar.** A general process supervisor in
   the compositor image restarts weston after every exit, a crash
   included, with its own crash policy. The kubelet's restart count
   and backoff then no longer show a weston crash loop, and only the
   supervisor's log does. The image is a library closure on scratch
   ([plan 01](../completed/01-the-compositor-image.md)), so the
   supervisor is one more program in that closure. This depends on
   the same three items as option 1, and on the choice of a supervisor
   and its crash policy.
3. **The kubelet's settings.** Two Kubernetes enhancements change the
   backoff, and both apply to every container on the node:
   - [KEP-4603, Tune CrashLoopBackOff](https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/4603-tune-crashloopbackoff),
     adds the feature gate `ReduceDefaultCrashLoopBackOffDecay`: a
     first wait of 1 s and a longest wait of 60 s. The gate is alpha
     since 1.33, and it is still alpha and off by default in 1.36.
   - [KEP-5593, Configure the max CrashLoopBackOff delay](https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/5593-configure-the-max-crashloopbackoff-delay),
     split from KEP-4603, adds `crashLoopBackOff.maxContainerRestartPeriod`
     to the kubelet's configuration, from 1 s to 300 s, behind the
     gate `KubeletCrashLoopBackOffMax`. The gate is beta and on by
     default since 1.35. A value under 10 s also lowers the first
     wait to that value.

   No enhancement in 1.36 sets the backoff for one container.
   KEP-4603 removed its per-pod `RestartPolicy: Rapid` in 2024 and
   lists it under its alternatives.
   [KEP-5307, Container Restart Policy](https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/5307-container-restart-policy),
   beta and on by default since 1.35, decides whether a container
   restarts by its exit code, and its text says that the restart still
   follows the exponential backoff. So it does not shorten the wait.
   This option depends on `liken`: the `Cluster`'s
   `spec.runtime.kubelet` renders a `KubeletConfiguration` today for
   the image collection settings only (`init/k3s.go`), so the setting
   needs a new field there. It also removes the backoff's protection
   from every workload on the node. A longest wait of 1 s makes every
   crash loop on the node restart once a second.
4. **Accept the delay.** The manual states that a second restart
   within about 10 minutes darkens every screen on the card for 10 s
   or more. This depends on nothing, except that `modeSwitchTimeout`
   must be at least as long as the backoff that can apply, or the
   prepare fails first.

## What is not known

- How the 30 s of each mode switch in the drill divides between the
  backoff, the kubelet's sync, weston's startup, and the readback.
- How often restarts come within 10 minutes of each other in real
  use. `display_compositor_restarts_total` counts restarts by reason,
  but nobody has read the intervals between them.
- Whether libweston can change the mode of a running output under the
  layout module. libweston 14.0.2 declares
  `weston_output_mode_set_native`, but nobody has tried it with the
  DRM backend. If it works, a mode switch needs no restart. A heal, a
  `hung` compositor, and a `masterless` one still need a new process.
- Whether the clients of each claim reconnect the same way after a
  restart inside the container as after a container restart.
