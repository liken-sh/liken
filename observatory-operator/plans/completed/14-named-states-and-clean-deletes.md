# 14, Named states and clean deletes

Proposed on 2026-10-06. Built on 2026-10-06, all three steps, and
tested against the fake API server and the fake INDI servers. The
drill on the test cluster has not run: it deletes the dust cap and the
dome during a session, and a telescope during a session, and reads the
Events. "What the build found" below records what a delete did before
the finalizers.

A park that fails or that a client aborts leaves `TELESCOPE_PARK` with
the state `Alert`. libindi's telescope also turns both switches off,
but a driver can leave `PARK` on. Step 1 reads an `Alert` park as not
parked, so such a mount reads `Stopped`, or `Slewing` or `Tracking`
when it still moves. The `Parked` condition still reads the switch
alone, and reads `True` for that driver.

## The problem

Two drills of plan 13 on the test cluster left two kinds of rough edge.

**Readings that mirror INDI's switches.** A `Mount` reports
`readings.parked` and `readings.tracking` as two booleans, because INDI
reports `TELESCOPE_PARK` and `TELESCOPE_TRACK_STATE` as two switches.
A mount is in one state at a time, so the pair admits combinations
that never happen, such as parked and tracking, and leaves out the
states a person wants to read, such as slewing. Three other readings
have the same smell:

| Kind | Today | What it hides |
|---|---|---|
| `Mount` | `parked`, `tracking` | one state in two fields; no slewing, parking, or unparking |
| `Dome` | `parked` | a dome that turns to its park position reads as not parked, with no sign that it moves |
| `Camera` | `exposureState` | INDI's light (`Idle`, `Ok`, `Busy`, `Alert`), where a person wants `Exposing` |
| `FlatPanel` | `light` | a boolean, where its action and its condition say `Lit` and `Dark` |

The `DustCap` already reports `cover: Open`, `Closed`, or `Moving`, and
the `Dome`'s shutter reports `Open`, `Closed`, or `Moving`. They are
the pattern for the rest.

Booleans that stay: a `Switch` output's `on`, a `GPS` `fix`, and a
`Guider`'s `calibrated` each answer one yes-or-no question.

**A deleted device leaves without its deactivation.** Plan 13 runs a
device's deactivation when it leaves an active parent, but a deleted
device stops with no deactivation, because its spec is gone when the
operator learns of the delete. A dust cap deleted during a session
stays open, and a dome deleted unparked stays open to the sky.

## The design

### Named states

Each reading that holds a state names it, in the words a person uses:

| Kind | Field | Values | From |
|---|---|---|---|
| `Mount` | `state` | `Parked`, `Parking`, `Unparking`, `Stopped`, `Slewing`, `Tracking` | `TELESCOPE_PARK`, `TELESCOPE_TRACK_STATE`, and the light of `EQUATORIAL_EOD_COORD` |
| `Dome` | `park` | `Parked`, `Unparked`, `Moving` | `DOME_PARK` |
| `Camera` | `exposure` | `Idle`, `Exposing`, `Done`, `Failed` | `CCD_EXPOSURE`'s light |
| `FlatPanel` | `light` | `Lit`, `Dark` | `FLAT_LIGHT_CONTROL` |

- `Stopped` is a mount that is unparked and not moving. A slew that
  ends in tracking reads `Slewing` until the coordinates settle.
- The mount loses the `Tracking` condition. Tracking turns on and off
  all session, so its condition posted an `Event` each time, and no
  procedure should wait on it. `readings.state` and its printer column
  show it. The mount keeps `Parked`, which the locks, `requires`, and
  the weather triggers read.
- The camera's `Cooling` condition stays: a cooler is on or off, and a
  `cool` action and a person both ask that question.
- The printer columns show the named state of each kind.

The API is `v1alpha1`, so the fields change with no conversion.

### A finalizer for each running resource

A device, a `Telescope`, or an `Observatory` gets the finalizer
`observatory.liken.sh/deactivate` while the operator runs something
for it. A device has it from just before the operator creates its pod
for a held server until its pod is gone and no held server runs it. A
`Telescope` has it from when a reservation takes it in `Wait` until
that reservation is `Released`, and an `Observatory` while a
reservation holds any of its telescopes. An inventory device, or a
telescope that no reservation holds, carries no finalizer, so
deleting it is instant.

- **A deleted device** that is connected to an active parent runs its
  deactivation, as a device that leaves does (plan 13, `leaves.go`):
  its spec is still in the store while the finalizer holds. A device
  with a `deletionTimestamp` is placed on no server, so the running
  server treats it as a device that left. Its deactivation runs only
  when its activation ran for the same transition of `Active`: a
  device deleted during activation, before the `Activation` step
  reached it, goes with no procedure. Then the
  operator stops its driver, deletes its pod and its `ResourceClaim`,
  and removes the finalizer. A failed deactivation posts
  `ProcedureFailed` and the cleanup goes on, as it does for a leave,
  so a broken driver cannot hold a delete forever.
- **A deleted `Telescope`** that a reservation holds ends that
  reservation: its deactivation steps run, as at `spec.end`, and the
  finalizer goes when the reservation is `Released`. The reservation
  records why it ended: the summary of its `Abort` step begins with
  `Telescope east was deleted`, and its `Deactivating` Event ends
  with the same words. A new reservation of a telescope that is being
  deleted waits in `Wait`, as it waits for a missing telescope.
- **A deleted `Observatory`** ends every reservation of its telescopes
  the same way.
- **The operator's own copy of the resource.** The operator reads the
  spec from the store, where a resource with a finalizer stays, with
  its `deletionTimestamp`, until the finalizer goes.

The finalizer follows the `Reservation`'s, which holds a deleted
reservation until its deactivation steps finish (plan 07). The cost is
the same: while the operator is down, a delete of a running resource
waits for it. A person can remove the finalizer by hand, as for a
reservation, and the README says what that skips.

### What this does not change

A device's own deactivation still runs only while its driver runs. A
pod that crashed or a node that went away cannot park anything, and
the finalizer does not wait for them.

## What the build found

The first test of step 3 ran against the operator before the
finalizer on `Telescope`: a fake API server, the example's resources,
and a `Ready` reservation of `east`. The delete removed the
`Telescope` at once. The reservation stayed `Ready` for the 30 minutes
the test waited, with its endpoint, and every pod kept running. The
runner's pass reads the missing telescope, finds nothing to keep, and
waits, so no step failed. On a cluster,
garbage collection would also delete the telescope's server pod,
which the `Telescope` owns, and leave the device pods with no server.
A delete of the reservation then ran the deactivation steps with no
procedure: `Deactivation` skipped with `missing Telescope east`, and
`StopSite` skipped, so the observatory's server stopped only through
the sweep. No deactivation procedure ran: the dust cap stayed open,
and the dome stayed unparked.

## The order of the work

1. The named states and the printer columns, with the `Tracking`
   condition removed. The README's table of readings.
2. The finalizer on devices, with their deactivation on delete.
3. The finalizer on `Telescope` and `Observatory`, after a test that
   shows what a delete during a session does today.

## How we test it

Unit tests in synctest bubbles: each named state from the property
combinations a driver sends, including a slew that ends in tracking
and a park that a client aborts; a device deleted during a session
runs its deactivation and goes; a device deleted while the operator is
down goes when the operator returns; a `Telescope` deleted during a
session ends its reservation and goes. The drill on the test cluster
deletes the dust cap and the dome during a session, and a telescope
during a session, and reads the Events.
