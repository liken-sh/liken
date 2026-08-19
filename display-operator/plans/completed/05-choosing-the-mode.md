# 05, Choosing the mode

Built and drilled on liken-1 on 2026-08-19, on top of plan 04: the
movie ran at 720x480 by claim alone, and the refusal event named
the full kernel list. Planned the same day. A claim asks for a
resolution, and the operator restarts the compositor to honor it.
The audio operator's plan 05 built the same claim shape for A2DP
codecs; this plan is the display analogue, with a bigger blast and
the design honest about it.

## What the metal established

The feasibility drill ran on liken-1 on 2026-08-19, with a
hand-written `weston.ini` on the claimed card:

* `mode=` in an `[output]` section matches the kernel's mode name
  byte for byte, so the `modes` attribute plan 03 published is
  already the vocabulary a claim uses. `mode=1280x720` brought the
  lab's portable panel up at `1280x720@60.0 16:9, current`.
* A bare width-by-height name lands on the kernel's first entry
  under that name, the highest refresh. Weston's aspect-ratio
  matching routes a flagged mode through its fallback slot, and the
  fallback still outranks `preferred`.
* A mode Weston cannot match falls back to `preferred` silently,
  with no log line. The operator must validate before writing and
  read the result back after, and can trust neither the log nor
  the exit status.
* The current mode reads back through the DRM `GETCRTC` ioctl on
  the card node this pod already claims, and the read works while
  the compositor holds DRM master. No debugfs, no privilege, no
  new dependency.
* `--drm-device` takes the card's name, not its path.

## The claim

The channel is the one DRA built for driver parameters, and the
audio operator already reads for codecs:

    spec:
      devices:
        config:
          - opaque:
              driver: display.liken.sh
              parameters:
                mode: "1280x720"

The driver reads the scheduler-resolved
`status.allocation.devices.config`, so a `DeviceClass` can carry a
mode as cluster policy and the claim's own choice wins by the
`source` field. `mode` is the only parameter, an unknown key fails
the prepare, and the value is a bare resolution name. Refresh
selection stays out until somebody asks: the `@refresh` syntax
parses integers only and interacts with the aspect-ratio flags, two
traps for no stated need.

## The prepare flow

Plan 04 makes this a flow one prepare call can complete, because
killing the compositor no longer kills the process that owes the
kubelet an answer.

1. Validate the requested name against the connector's own sysfs
   mode list, never against the published attribute. The attribute
   cuts at 64 characters and drops real modes, so the attribute is
   the advertisement and sysfs is the truth. The failure names the
   full list, which is the one place a person sees the names the
   attribute could not carry.
2. Read the connector's current mode with `GETCRTC`. A match
   delivers at once, which makes the kubelet's retry free and the
   whole flow idempotent.
3. Record the mode and rewrite `weston.ini`. The record is a small
   file beside the config in the same pod-local volume, one entry
   per connector, and the config regenerates from the connector
   walk plus the record, so the ini is always derived and never
   parsed back. The operator gains the config volume mount that
   today only `declare` and the compositor hold.
4. End the compositor. The pod shares its process namespace, so
   the operator finds the one process running `weston` and sends
   it `SIGTERM`. The kubelet restarts the container, the new
   compositor parses the rewritten config, and every client on the
   card loses its connection, which is the accepted cost.
5. Wait, bounded, for the socket to answer and for `GETCRTC` to
   report the requested mode. The drill measured 134 milliseconds
   of compositor startup and about a second of kubelet turnaround,
   so a ten-second bound has room. On the readback matching,
   deliver.

One guard: a restart budget. When the rewritten config already
asks for this mode and a restart already happened, a readback
mismatch means Weston declined the mode, and a second restart
would blank the machine's screens for the same wrong answer.
Prepare fails without restarting again, and the failure says the
compositor declined the mode.

## What survives what

The rewritten config lives in the pod's config volume, so a
compositor restart applies it and a pod restart erases it. That is
the right death: when the pod restarts, the kubelet re-prepares
the claims of every consumer that comes back, and the re-prepare
rewrites the mode and restarts the compositor if the mode is not
already up. A machine that reboots with no consumers left comes up
at `preferred`, which is what an unclaimed screen should run.

Unprepare restarts nothing, the same choice the codec plan made
and for the same reason: the device allocates to one claim at a
time, and a revert would restart every screen on the machine to
serve nobody. It does remove the connector's entry from the record
and the ini, so the running mode outlives the claim only until the
next compositor start, which comes up at `preferred`. The leftover
is visible instead of hidden: every
reconcile pass publishes a `currentMode` attribute from `GETCRTC`,
so the slice always says what each output runs right now.

## The blast, stated plainly

A mode request on one connector ends every Wayland client on every
connector of that card, for about 1.3 seconds of dark plus each
consumer's own restart. The drill measured a bare pod dying
`Completed` and staying dead. The manual's claim guide must say:
run a display consumer under a controller, and expect every screen
on the machine to blink when any claim on it states a mode.

## The drill

On liken-1, with the movie demo as a Deployment:

1. The movie claim states `mode: "1280x720"`. The portable panel
   comes up at 1280x720, the slice's `currentMode` follows, and
   the ultrawide blinks and returns.
2. Delete and reapply with no mode. The panel stays at 1280x720,
   `currentMode` says so, and nothing restarts.
3. State `mode: "640x480"`, a name the panel's sysfs list carries
   but the attribute dropped. It works, proving validation reads
   sysfs.
4. State `mode: "1234x567"`. The pod parks and the event names
   the full list.
5. Kill the compositor container's process mid-movie. The kubelet
   restarts it alone, the mode comes back from the rewritten
   config, and the movie pod, under its controller, returns on its
   own.
