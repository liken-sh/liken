# StartSite holds the observatory's lock while it waits for a pod

Open design question, recorded on 2026-11-19. A review found it with a
probe test, and a unit test reproduced it on 2026-11-19.

Each telescope of an `Observatory` has its own `Reservation`, and the
observatory's server and devices, such as the dome and the weather
station, are shared by all of them. The `StartSite` step of a
reservation takes the observatory's lock, and then waits for the pods
of the observatory's devices to be `Ready`, up to the step's deadline
of 10 minutes. While it waits, no other reservation of the same
observatory can do work that needs the lock.

So when a person ends the night on one telescope while a second
telescope starts and a pod of the observatory is `Pending`, the first
`Reservation` stays in its `Deactivation` step, and its mount is not
parked, until the second reservation's `StartSite` times out. The
review measured the same delay with an ended pod: with one telescope
`Ready`, the observatory's server pod waiting for its replacement, and
a second telescope in `StartSite`, the first telescope's `Reservation`
"was released 9m55s after its deletion".

## The reproduction

A test in a `synctest` bubble used the fake world of the existing
tests, `startWorld`. It made `east-tonight` `Ready`. It then made the
fake API server hold the dome's pod `lab-dome` `Pending`, deleted the
pod, and waited until the operator created it again, `Pending`. It
reserved `west-tonight`, the observatory's other telescope, and
waited until its `StartSite` step was `Running`. Then it deleted
`east-tonight` and measured the time on the bubble's clock until the
`Reservation` was gone. It asserted that the release took less than a
minute. It failed in 20 of 20 runs with:

```
the Reservation east-tonight was released 10m0s after its deletion, while the StartSite step of west-tonight waited for the Pending pod lab-dome, want within a minute
```

The operator's log of the run shows `StartSite timed out after 10
min: waiting for pod lab-dome of Dome lab (Pending)` for
`west-tonight`, and then `Deactivation done in 10 min` for
`east-tonight`.

The test is not in the tree, because a failing test cannot land on
`main`.

## The cause

`startSite` (`activation.go:206`) takes the lock at
`activation.go:213` and releases it when the step returns. Between
the two, it calls `startServer`, and then `startAndConnect`, which
waits in `waitReady` (`topology.go:135`) and in `waitDefined`
(`topology.go:180`), and `waitDefined` waits in `waitServer`
(`topology.go:160`). Each wait runs `waitFor` (`operator.go:360`),
which ends only on its check or on the step's context.

Each of these takes the same lock and waits for it:

- `deactivation` (`procsteps.go:323`), which runs the dome's park
  procedure and parks the mount.
- `stopSite` (`deactivation.go:319`).
- `keepPods` of the steady state (`steady.go:131`), and
  `reapplySite` through `locksOf` (`steady.go:442`).

## The decision

How should `StartSite` wait for a pod, so that another reservation's
end is not delayed?

- **Release the lock around each wait.** `StartSite` takes the lock to
  read the observatory and create its pods, releases it while it waits,
  and takes it again to connect the devices and write their settings.
  Another reservation can then stop the observatory during the wait,
  so after it takes the lock again, `StartSite` must read the
  observatory again and start what is gone. It removes the delay in
  every case.
- **A release of another reservation cancels the wait.** When a
  reservation of the same observatory needs the lock to end, the
  waiting `StartSite` ends early and runs again after the other
  reservation releases the lock. The delay goes, but the second
  reservation's `StartSite` starts again from the beginning, and the
  operator needs a way to tell the waiting step that a runner asks for
  the lock.
- **Leave it and document it.** The manual says that a reservation's
  end can wait up to 10 minutes, the `StartSite` deadline, while
  another telescope of the same observatory starts and a pod of the
  observatory does not become `Ready`. A mount that must be parked
  stays unparked for that time.
