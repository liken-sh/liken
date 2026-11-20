---
name: receiver-drivers
description: Adds or changes a setting, a settings family, a zone control, or a protocol block of a Receiver in equipment-operator, under the settings model of equipment-operator plan 14. Covers where a setting goes (spec.settings for a meaning that every brand shares, the brand's block for the rest), the tier of each field (Confirmed, Sent once, Reported, Unsupported), what a device's reply does to a field, status.settingExceptions and the SettingsConfirmed condition, when an undocumented setter can be declared, and the steps to write a driver for a new receiver brand. Use before adding or changing a receiver setting, before writing a driver for another receiver brand such as a Yamaha, an Onkyo, or an Arcam, and when reviewing a change to a driver's settings.
---

# Receiver drivers

A `Receiver` has one protocol block, such as `spec.denon` or
`spec.wiim`. Each block has a driver package in `equipment-operator/`
that implements `equipment.Driver`. When a person replaces a
receiver with another one, the room's setup stays the same only if
each setting means the same thing, in the same shape, on every brand.
So every receiver setting follows the rules below.
[Plan 14](../../../equipment-operator/plans/14-one-settings-model-for-every-receiver.md)
of `equipment-operator` has the reasons, the common vocabulary and its
units, the survey of receiver protocols, and the references for a new
brand.

## Which section to read

- **A setting or a zone control on a brand that has a driver:** every
  section except "Add a receiver brand".
- **A driver for another brand:** every section.

## What the CRD declares and what plan 14 adds

Plan 14 designs fields that the CRD may not have yet. So read
`equipment-operator/deploy/receivers-crd.yaml` before you write a
field.

The CRD declares the settings of each brand in its block, such as
`spec.denon.settings`, and the zone controls in `spec.zones`. For each
declared field, the operator compares the declared value with the
value that the receiver reports. It sends the field only when the two
differ, and at most 3 times for one value (`sendLimit` in
`send_budget.go`). When the receiver does not report a field, the
operator sends it once each time the spec changes that block.
`status.settledSettings` records a digest of each block
that the operator sent. So after a restart, the operator sends
nothing for a block that nobody changed. A method of `receiverUnit` in
`reconcile.go` applies each block, such as `setSettings` for the
Denon block.

Plan 14 also designs `spec.settings` and `status.settings` for the
common families, `status.settingExceptions`, a table of fields with
the tier of each field in each driver, one digest for each family,
and one engine that applies every block. Read the CRD to learn which
of these exist. When the CRD does not declare one yet, build the new
field so that it can move into plan 14's design with no change of
meaning.

## Where a setting goes

Read the CRD first, because it decides where a setting goes. When
the CRD has `spec.settings` with the setting's family, the setting
goes there. When the CRD has no `spec.settings`, or no family for the
setting, the setting goes in the brand's block, such as
`spec.denon.settings`, and the common family waits for the plan that
builds it.

- A setting that means the same thing on every brand belongs in the
  `Receiver`'s `spec.settings`. It uses one vocabulary, with explicit
  units, so a person learns one shape, and a new receiver keeps the
  room's setup. Plan 14's table "The families and their units" lists
  the families and their fields.
- The protocol block contains the connection and the settings that
  only that brand has. A `Receiver` has exactly one protocol block,
  and the CEL rule `exists_one` in the CRD enforces this.
- The status follows the same layout as the spec: `status.settings`
  for the common values, and `status.<protocol>`, such as
  `status.denon`, for the brand's values.
- A plan decides which families get built. Look in
  `equipment-operator/plans/` for the plan that adds the family. When
  no plan covers the family, write one first, with the `plans` skill.

## The tiers

Plan 14 gives each field one tier. In `denon/`, `settingsTable` in
`settings.go` joins one list of `settingSpec` for each family, such
as `toneSettings` in `settings_tone.go`. A `settingSpec` has the
field's id, the function that builds its command, and the function
that copies it, and no tier. When a driver's table records no tier,
write the tier of a field in the description of its CRD field.

The operator learns what a model supports from the model itself,
never from a table of models, because support differs by model and
not only by protocol.

- **Confirmed**: the receiver reports the field's value. The
  operator compares it, sends the field when it differs, and confirms
  the new value.
- **Sent once**: a documented setter with no way to read the value.
  The operator sends it once for each change of the family.
  `status.settledSettings` keeps the operator from sending it again
  after a restart.
- **Reported**: a value that can be read but has no setter. It is
  only in the status and has no spec field.
- **Unsupported**: the model rejected the read or the command, left
  it out of its capability document, or did not reply to it during
  the first read.

The tier of a field is the highest tier that the field can reach.
When a unit does not report a Confirmed field's value, the operator
sends that field once on that unit. The tier goes in the CRD field's
description and in the reference, because it is the same for every
unit of a protocol. The status lists only the fields whose state
differs from their tier.

## What a device's reply does

A driver maps each reply of its device to one of plan 14's answer
kinds:

- **Refused**: the command does not exist on this model, such as a
  WiiM's `unknown command`. The field is Unsupported until the next
  connect.
- **Not now**: the command is valid, but not in the device's current
  state, such as a command to a zone in standby. It counts against
  the limit of 3 sends, and it never makes a field Unsupported. A
  driver that made such a field Unsupported would stop sending a
  field that the model has.
- **Silent**: the device sends no error, as a Denon does. When a
  query has no reply by the end of the first read, the field becomes
  Unsupported, with a reason that says so.

When the device publishes a capability document, the driver reads it
before it probes. A probe of a protocol that sends no errors takes
the whole first read. A field that the document leaves out is
Unsupported, and the reason names the document.

## The exceptions and the condition

`status.settingExceptions` lists each field whose state differs from
its tier, with the reason. When the device sent its own text, the
reason includes that text. When a declared field is in the list, the
`SettingsConfirmed` condition is `False`, and the condition's message
lists the paths of those fields.

The operator never changes an undeclared field so that a declared
field can take effect. When a declared field depends on another
setting that the person controls, and that setting is not as the
field needs, the field is listed as Blocked.

## What can be declared

- An undocumented setter can be declared only after a drill shows
  that the receiver reports the value back. The same applies to a
  setter that only a client library documents.
- A command that acts once, such as a reboot or an alarm, is not a
  setting, and it gets no spec field.

## Add a setting

1. Check that the setting does not exist. Search the CRD and the
   driver's package for it, for example for the bass of a Denon:

   ```sh
   cd equipment-operator
   grep -n -i bass deploy/receivers-crd.yaml denon/*.go
   ```

   A zone control, such as a zone's volume, is under `spec.zones` and
   `spec.volume` in the CRD. When the field exists, the task is a
   change to it, and not a new field.
2. Find the plan that covers the setting's family. In it, find the
   row with the setting's setter, its read, its values, and its tier.
3. Find where the command comes from in the driver's protocol
   reference: the `AGENTS.md` of the driver's package, such as
   `denon/AGENTS.md`, and the files it links to. For another brand,
   plan 14's table "References for the next protocol block" lists
   the documents and the client libraries.
4. Add the field to the CRD, with a description that gives the tier,
   the values, and the units. Then run `make -C docs generate` in
   `equipment-operator`, because the reference pages are generated
   from the CRD.
5. Add the field to the driver's types, its codec, and its read. Map
   the device's replies to the answer kinds.
6. Test it against the driver's fake peer, which replies from a real
   device's transcript in `testdata/`. The `testing` skill describes
   the fakes.
7. Update the manual pages that the `manuals` skill lists for a
   change to a resource's spec or status.

## Add a receiver brand

Follow the newest driver as the model, because it has each part that
the controller expects of a driver. Each driver has a plan in
`equipment-operator/plans/completed/` whose title names the driver,
such as `06-wiim-driver.md`. The plan with the highest number is the
newest driver:

```sh
ls equipment-operator/plans/completed/ | grep -i driver
```

Read that driver's package and its plan. Then find each other file
that names the driver. Search the component's Go files for the name
of the newest brand, ignoring case, and leave out the brand's own
package:

```sh
cd equipment-operator && git grep -n -i wiim -- '*.go' ':!wiim/'
```

Read each hit. Each hit in a file that is not a test is a place that
your brand needs too, such as a comparison in `status.go` or the
metrics in `metrics_wiim.go`.

1. Write a package `<protocol>/` in `equipment-operator` that
   implements `equipment.Driver`. It imports `equipment/`, and never
   the controller. It contains the brand's settings type, its table
   of fields, such as `settingsTable` in `denon/settings.go`, its
   codecs, its status type, and its mapping of replies to the answer
   kinds.
2. Write an `AGENTS.md` in the package, as `wiim/AGENTS.md` does.
   Put in it the protocol references, the command families, the
   command behind each status field, and what a measured unit
   replied.
3. Where the model has a capability document, read it before any
   probe. For a protocol that does not report the model, `upnp/`
   reads the manufacturer and the model from a UPnP device
   description.
4. Write a fake peer that replies from a real device's transcript in
   `testdata/`, such as `denon/testdata/avr-x1700h.txt`. Use
   `net.Pipe`, or an `http.Handler` on an in-memory transport.
5. Add the protocol block to `api.go`. The block is a type
   `<Brand>Protocol`, such as `WiimProtocol`, with the device's
   identity, its address, and its `Settings`, and a field of that type
   in `ReceiverSpec`. Add the status block `status.<protocol>`, such
   as `status.wiim`, as a field of `ReceiverStatus` whose type is the
   package's status type. Then set it in `buildReceiverStatus` and
   `protocolName` in `status.go`.
6. Add the block to the CRD and to its `exists_one` rule. Start the
   driver in `startDriver` in `reconcile.go`, and apply its settings
   block with a method of `receiverUnit`, such as `setWiimSettings`.
   Add the block to `declaredBlocks` in `settled.go`. Add the package
   to the `Dockerfile`, because `TestTheDockerfileCopiesEveryGoPackage`
   fails when the image build does not copy it.
7. Decide whether the operator discovers the device. Only the WiiM
   driver has discovery. `discovery.go` calls `wiim.Discover`, which
   searches with mDNS and SSDP, and keeps the current address of each
   device by its UUID. It creates a `Receiver` with the label
   `equipment.liken.sh/discovered` for each device that no `Receiver`
   names. So a driver needs two things for discovery: an identity in
   its block that the device reports, such as `spec.wiim.uuid`, and a
   search that returns that identity with an address. `discovery.go`
   names only `wiim` types, so a second brand that discovers changes
   it. A driver with no discovery, such as `denon/`, takes a declared
   `address`, and the operator resolves a host name.
8. Read `deploy/receivers-crd.yaml` for `spec.settings`, as the first
   step of "Add a setting" does. When the CRD has `spec.settings` with
   a family, add a row at the tier that the protocol's documents
   support for each field of that family that the protocol can set
   or read. When it does not, put each setting in the brand's block.
9. Prove the block on a real receiver. Until then, a setter that no
   document describes cannot be declared.
