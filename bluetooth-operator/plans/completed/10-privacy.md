# Privacy

Plan 10. Built 2026-10-07. Drilled on `liken-1` on 2026-10-07: steps
1 to 3 ran on the radio on `stick-1`, and step 4, which sets the field
back to `off`, has not run. The results are in
[What the drill measured](#what-the-drill-measured).

It lets a person turn on Bluetooth Low Energy privacy for one radio
with a field on its `Adapter`, and it keeps the radio's identity key in
a `Secret`, so a reinstall or a moved radio keeps the identity that its
peers know. Privacy stays off unless a person sets the field.

This plan replaces the open problem "The `Secret` does not copy the
adapter's `identity` file", which is deleted in the same commit.

A claim about BlueZ was read in the 5.82 tree, which is the version the
`bluetoothd` image builds. A claim marked "measured" was run on
`liken-1`.

## What privacy does

A radio with privacy off advertises and connects from one fixed
address, so anybody in range can recognize it each time it appears.
With privacy on, the radio uses a resolvable private address that it
changes every few minutes. The address comes from the radio's identity
resolving key (IRK). Two devices exchange their IRKs when they pair, so
a bonded peer resolves each new address back to the radio, and a
device that never paired with it cannot.

Privacy applies to Low Energy links only. A classic link always uses
the radio's public address.

The setting belongs to the radio, not to a peer. bluetoothd reads one
`Privacy` key from `main.conf` (`parse_privacy` in `src/main.c`), and
when each adapter starts it sends that value to the kernel with the
adapter's IRK (`set_privacy` in `src/adapter.c`, called from
`read_info_complete`). The IRK is in `<adapter>/identity` under
`[General] IdentityResolvingKey`. `load_irk` reads it, and writes a new
random key when the file has none. bluetoothd sets a per-peer flag,
device privacy mode, on every peer it adds to the kernel's connect
list. That flag controls which addresses the radio accepts from that
peer, and bluetoothd sets it from the same `Privacy` value, so a
`PairingRequest` or a `Peripheral` needs no field of its own.

The kernel takes `MGMT_OP_SET_PRIVACY` only while the radio is powered
off, and bluetoothd sends it only when the adapter starts. So a new
value takes effect when bluetoothd starts again.

Today the operator does not set the key, and bluetoothd's default is
`off`. The `bluetoothd` image ships `btmgmt`, and `btmgmt privacy on`
in a `kubectl exec` session turns privacy on until bluetoothd starts
again.

## The design

### The field

`Adapter.spec.privacy` takes the five values of BlueZ's `Privacy` key,
spelled as BlueZ spells them: `off`, `network`, `device`,
`limited-network`, and `limited-device`. An empty field is `off`. The
CRD's schema states the enum, so the API server refuses any other
value. BlueZ reads a value it does not recognize as `off`, and the
schema keeps that silent fallback from a typing error.

The field description gives each value's meaning in one sentence, from
the comment in BlueZ's own `main.conf`.

### From the field to bluetoothd

`bondfetch` runs before bluetoothd and already holds an API client and
the radio's address. It reads the `Adapter` named for that address and
writes the value of `spec.privacy` into a file in a new `emptyDir`,
`settings`, mounted at `/var/run/bluetooth.liken.sh/settings`. An
`Adapter` that does not exist yet is `off`, because the operator
creates it on its first pass. Any other failure to read the `Adapter`
exits nonzero, the same as a failure to read the bonds, so the pod
stays in `Init` and does not start with a value that a person did not
choose.

`start-bluetoothd` reads that file and writes `/etc/bluetooth/main.conf`
at start, with `AutoEnable=true` from the baked file and the `Privacy`
key. It refuses a value outside the five, the same way it refuses a
bad `BLUETOOTH_CLASSIC_BONDED_ONLY`. A missing file is `off`, so a
deployment without the `settings` volume keeps today's behavior.

The file records the value that bluetoothd started with. A restart of
the `bluetoothd` container alone reads the same file again, so the
file and the running daemon agree until the pod is replaced.

### A change to the field

The operator's container mounts `settings` read-only. On each pass it
compares `spec.privacy` with the file. When they differ, it stores the
bonds once more, so a pairing from the last seconds reaches its
`Secret`, posts an `Event` on the `Adapter` with the reason
`PrivacyChanged` that names both values, and deletes its own pod. The
`DaemonSet` creates a new pod, and `bondfetch` writes the new value.

The pod's name comes from the downward API in `POD_NAME`. The `Role` in
the operator's namespace gains `delete` on `pods`. RBAC cannot narrow
the verb to one pod name that changes with each pod, so the comment in
`rbac.yaml` states the scope and the reason.

The restart drops every connected controller for the few seconds the
pod takes to start, and the controllers reconnect as they do after any
roll of the pod. A drill on `liken-1` on 2026-10-07 restarted the pod on
`stick-1`, and audio-operator read the bus again within three seconds
(measured).

### The identity key

The adapter's `identity` file goes into its own `Secret`,
`bluetooth-identity-<adapter>`, in the operator's namespace. The
`Adapter` owns it, so deleting the `Adapter` collects the key with the
bonds. It carries a label that `bondfetch`'s bond selector does not
match, so it is never read as a bond.

The store pass that writes bonds back (`bondstore.go`) also reads
`<adapter>/identity` and writes the `Secret` when the file differs from
it. `bondfetch` writes the file back into the tree before bluetoothd
starts. A radio with privacy off has no file and no `Secret`.

When privacy is on and no key is stored, `bondfetch` writes a new
random key, and the store pass then writes it into the `Secret` once
in the radio's life. bluetoothd can make the key itself, but it draws
the random bytes through the kernel's `AF_ALG` socket, and Ubuntu's
kernel builds that socket's support as modules. On `stick-1` on
2026-10-07, which loads none of them, bluetoothd logged
`generate_and_write_irk() Failed to open crypto` and started with
privacy off (measured). A key from `bondfetch` needs no kernel crypto.

The `Secret` is kept when privacy is turned off again. The key costs
nothing while privacy is off, and a radio that turns privacy on again
presents the identity its peers already know.

### Status

`Adapter.status.privacy` reports the value in the `settings` file,
which is the value the running bluetoothd started with. A printer
column shows it. The field reports what bluetoothd was told, not what
the kernel accepted. `set_privacy_complete` logs a refusal from the
kernel and stores nothing, and the operator has no management socket
to read the setting back. The drill reads it with `btmgmt info`.

## What a person has to know

A peer that paired while privacy was off may not hold the radio's IRK.
If it does not, it cannot resolve the radio's private address after
privacy turns on, and a Low Energy controller that filters by its bond
may refuse the connection until it pairs again. This is not proven
either way. The drill measures it, and the manual states the result.

## The manual

The `Adapter` reference page documents the field, the status field,
the printer column, the `Event`, and the restart. A section of the
concepts page explains what privacy hides and what it costs, in the
words of [What privacy does](#what-privacy-does). The pairing guide
says which links privacy affects.

## How it will be proved

Tests, on the Go toolchain alone:

- `start-bluetoothd` writes each of the five values into `main.conf`,
  writes `off` with no file, and refuses any other value.
- `bondfetch` writes the field's value and the stored `identity` file,
  writes `off` for an `Adapter` that does not exist, and exits nonzero
  when the read fails.
- The operator deletes its own pod once when the field differs from
  the file, after a store pass, with one `PrivacyChanged` `Event`, and
  does nothing when they agree.
- The store pass writes the `identity` `Secret` when the file appears
  and when it changes, and never reads it as a bond.

The drill, on `liken-1`, on the radio on `stick-1`:

1. Set `spec.privacy: device`. The pod rolls once, `status.privacy`
   reads `device`, and `btmgmt info` lists `privacy` in the current
   settings.
2. The `bluetooth-identity-` `Secret` appears. Roll the pod by hand,
   and the `identity` file in the new pod holds the same key.
3. Press a button on each paired controller, the DualSense on a
   classic link and the X6, and record whether each reconnects
   without pairing again. This step needs a person at the controllers.
4. Set the field back to `off`, and confirm the roll and the status.

## What the drill measured

On `liken-1`, on 2026-10-07, on the radio on `stick-1`, with build
`50b3d3ac` (measured):

1. Setting `spec.privacy: device` rolled the pod once, and the
   `PrivacyChanged` `Event` reached the API server before the old pod
   ended. `status.privacy` read `device`. The `btmon` trace showed
   bluetoothd send `Set Privacy` with `Privacy: Enabled (0x01)` and a
   nonzero key.
2. The operator stored the key in `bluetooth-identity-f4-96-34-aa-91-bd`.
   After a roll of the pod by hand, `bondfetch` restored the file, and
   the new bluetoothd sent the same key.
3. The X6, a Low Energy remote paired while privacy was off, connected
   again on a button press without a new pairing. The radio set a
   resolvable random address and connected to the X6 with own address
   type `Random (0x03)`. The radio connects to the X6, so the X6 did
   not have to resolve the radio's address to accept the link. A Low
   Energy controller that connects to the radio itself was not tested.
   The DualSense, on a classic link, kept working, as a classic link
   does not use privacy.

The first build, `d235281c`, sent `Privacy: Disabled (0x00)` with a
zero key, because bluetoothd could not make the key (see
[The identity key](#the-identity-key)).

## What was considered and set aside

- **One flag for the whole `DaemonSet`**, like
  `BLUETOOTH_CLASSIC_BONDED_ONLY`. It is simpler, but every radio gets
  the same value, and the `Adapter` already holds the other setting a
  person makes about a radio, its alias.
- **Applying the value without a restart.** The kernel takes the
  privacy command only while the radio is powered off, and bluetoothd
  sends it only at adapter start. The operator could power the radio
  off over D-Bus and send the command through the management socket,
  but it holds no management socket, the `bluetoothd` container would
  have to share one, and connections drop either way.
- **Applying the value at the next pod restart only.** A change would
  then wait for a node drain or an upgrade, with no sign of when it
  takes effect, and status would disagree with spec in the meantime.
- **A liveness probe that fails when the value changes.** The kubelet
  would restart only the `bluetoothd` container, but each restart
  counts toward the crash backoff, and a probe is a timer that reads
  state again.
