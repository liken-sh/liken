# The compositor can start without DRM master

Open problem. After a machine rebooted into a new liken release, the
compositor on that machine came up without DRM master. Every commit to
the output failed, the panel showed the kernel console instead of the
idle screen, and nothing restarted the compositor. The liveness probe
passed the whole time. Deleting the operator's pod on that node fixed
it at once: the new compositor took the output and drew normally.

## What was seen

A home cluster rolled its nine machines to liken 2026.09.27-001, one
reboot at a time. On one screen machine, whose only connected output
is `HDMI-A-2` into an AV receiver, the panel showed the console after
the reboot. The other eight compositors on the fleet were healthy.

The compositor's log, from its start after the reboot:

```
display.liken.sh: the compositor takes card1
[21:26:53.136] weston 14.0.2
[21:26:53.136] Command line: /usr/bin/weston --backend=drm --drm-device=card1 --config=/etc/weston/weston.ini --socket=wayland-0
[21:26:53.137] OS: Linux, 7.2.6-070206-generic, ...
[21:26:53.155] initializing drm backend
[21:26:53.155] Trying libseat launcher...
[21:26:53.156] [libseat/libseat.c:62] Seat opened with backend 'noop'
[21:26:53.156] libseat: session control granted
[21:26:53.157] using /dev/dri/card1
[21:26:53.157] DRM: supports atomic modesetting
[21:26:53.658] Loading module '/usr/lib/x86_64-linux-gnu/libweston-14/gl-renderer.so'
[21:26:53.675] Using rendering device: /dev/dri/renderD128
[21:26:53.877] atomic: couldn't commit new state: Permission denied
```

After that first failure, 0.7 seconds after the compositor opened the
card, every repaint logged the same pair of lines, several times a
second, until the pod was deleted about six minutes later:

```
[21:32:00.466] atomic: couldn't commit new state: Permission denied
[21:32:00.466] repaint-flush failed: No such file or directory
```

The operator container in the same pod logged, over and over:

```
placing the surfaces on each screen: HDMI-A-2: the layout module refused "place 2 HDMI-A-2 0 0 1920 1080 none 0": no such output
the layout module refused "order HDMI-A-2 2": no such output
drm change: /devices/pci0000:00/0000:00:02.0/drm/card1
slice: wrote generation 3, 4 devices, 2 tainted: hdmi-a-2 changed attributes
```

Each container of the pod showed one restart, from the node's reboot,
and every container reported ready. The media browser, which draws the
idle screen as a Wayland client of this compositor, ran normally and
drew into a compositor that could not show its frames.

After `kubectl delete pod` on the operator's pod for that node, the new
compositor logged no `Permission denied` line:

```
[21:32:47.454] DRM: head 'HDMI-A-1' found, connector 390 is disconnected.
[21:32:47.481] DRM: head 'HDMI-A-2' found, connector 400 is connected, EDID make ...
[21:32:47.481] Output 'HDMI-A-2' enabled with head(s) HDMI-A-2
[21:32:47.508] liken-layout: listening on wayland-... for output HDMI-A-2 on descriptor 37
```

The two-machine testbed rolled to the same release an hour earlier,
and both of its compositors came up with master.

## Why the compositor could not recover

The compositor runs with no capability, and libseat's `noop` backend
opens the card node with a plain `open()` (the `weston` container's
security context in `deploy/operator.yaml` says so). The kernel makes
the first process that opens a DRM card node its master. A process
without `CAP_SYS_ADMIN` cannot call `DRM_IOCTL_SET_MASTER` to take
master later. So a compositor that was not master at its open stays
without master for its whole life, and every atomic commit fails with
`EACCES`.

## Why nothing restarted it

`probeCompositor` (`weston.go`, called from `main.go` and
`placementpass.go`) connects to the compositor's socket and completes a
`wl_display.sync` round trip. That proves the compositor answers its
clients. A compositor without master still answers its clients, so the
probe passed. mpv's window watchdog (exit 7 on a lost window) covers a
compositor that goes away, not one that stays and cannot show frames.
No check reads whether the compositor's commits succeed.

## Two hypotheses, not yet told apart

1. **Another process opened the card first.** Something opened
   `/dev/dri/card1` before 21:26:53.157 and still held it open, so it
   held master. Candidates: another container in the pod (the
   `weston` sidecar starts first, but the pod's containers all
   restarted after the node's reboot, and the restart order is the
   kubelet's), a probe in the operator that opens the card node to
   read connectors, or a process outside the pod.
2. **A process took master and closed.** A process opened the card
   node before the compositor, became master, and closed it. With no
   open master, the kernel does not make the next opener master in
   every kernel version; the compositor then opened a card with no
   master and could not take it. The rule for who becomes master when
   the old master closes has changed across kernel versions; check it
   for the kernel this fleet runs (7.2).

To tell them apart the next time: while the compositor logs
`Permission denied`, read `/sys/kernel/debug/dri/<minor>/clients` on
the node (it lists each open file of the card, its pid, and which one
is master), and `ls -l /proc/*/fd | grep dri/card1` to name every
process that holds the card open.

## What would fix it

- **Detect it.** Make the compositor's liveness depend on a commit
  that worked, not only on an answer to its clients: for example, fail
  the probe when the compositor's log or a counter shows repeated
  `couldn't commit new state` failures, or when the operator sees that
  a connected output has no compositor output (the `no such output`
  refusals above). A failing probe makes the kubelet restart the
  compositor, which is exactly what fixed it by hand.
- **Prevent it.** Find the process that opened the card first, and
  make it open the card without taking master (for example through
  the render node, or after the compositor holds master), or make the
  compositor start before anything else can open the card node on
  that node.

Detecting it is the smaller change and covers any cause. Preventing it
needs the cause first.
