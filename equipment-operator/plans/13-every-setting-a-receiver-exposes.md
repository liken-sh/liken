# 13, Every setting a receiver exposes

Proposed on 2026-10-01 as "The receiver's whole setup", for the Denon
speaker setup. Rewritten on 2026-10-04 to cover every setting of every
`Receiver`, through every protocol the receiver serves. It replaces
plan 07's open HTTP design, the open problem about the WiiM settings
the amp does not read back, and the open problem about the
`Receiver`'s `cec:` block. Not built.

## The problem

A person who sets up a `liken` home theater has to read and change
the receiver's settings, and today has to walk to the receiver for
most of them. `equipment-operator` covers some families on each
protocol, and the coverage has no rule:

* A Denon `Receiver` reads and sets six families over the control port:
  `system`, `tone`, `audyssey`, `audio`, `hdmi`, and `channelVolumes`.
  The speaker setup is missing: the configuration, the sizes, the amp
  assignment, the distances, the crossovers, the calibrated levels, and
  the subwoofer mode. So are the input names and visibility, the quick
  select names, and the second zone's setup. The video controls, the
  tuner, the network player, and the triggers are not read.
* A WiiM `Receiver` reads and sets two families: `device` (name,
  lights, buttons) and `audio.balance`. Its equalizer, tone, output
  mode, and subwoofer have documented setters, and the operator does
  not model them.
* A receiver on a CEC bus can take System Audio Mode, and only CEC
  reaches it. The `Receiver` has no `cec:` block.

The gap showed during the speaker walk of audio-operator's plan 12 on a
home cluster. The walk's side and back windows both played on the
surround speakers, and the reason was in the receiver's on-screen menu:
the receiver was set up as 5.1 with two height speakers, assigned as
Top Middle. A `Receiver` status that showed the speaker setup would
have answered the question in one `kubectl get`.

The coverage also differs between the two protocol blocks with no
reason. Denon names its unit-wide family `system`, and WiiM names the
same kind of family `device`. Denon's tone is a family, and WiiM's tone
is not modeled. A person who has both receivers learns two shapes.

## The rule

This plan sets the rule for every protocol block of every kind this
operator serves, now and later: `denon:`, `wiim:`, `cec:`, and the
HEOS, PJLink, and serial blocks that `00-design.md` leaves for later.

**A `Receiver` exposes every setting the receiver serves on any
protocol it exposes.** The driver reads every family the receiver
answers into status, and the spec accepts every setting the operator
can send and confirm. The operator learns what a model supports from
the model, not from a table of models.

Each field has one tier, which the driver's family table states:

| Tier | Meaning | How the operator treats it |
|---|---|---|
| Confirmed | The receiver reads the field back. | Declarable. The operator compares, sends a field that differs, and tries one value at most 3 times. This is today's mechanism. |
| Sent once | A documented setter, with no read. | Declarable. The operator sends it once for each change of the block, and after a restart only when the block differs from `status.settledSettings`. This is today's mechanism for a field the receiver does not report. A change made at the receiver is not seen, so the operator does not send the field again. |
| Reported | A read, with no setter. | Status only. |
| Unsupported | The model refuses the read and the write. | Listed in `status.<protocol>.unsupported`, by family and field. |

Three conditions move a field between tiers:

* An undocumented setter is declarable only after a drill shows the
  receiver reads it back at the value it was sent. It is then
  Confirmed. An undocumented setter that has no read stays out of the
  spec, because nothing can prove it did anything.
* A documented setter whose read fails on a model is Sent once on that
  model. The read's failure is the model's answer, so the driver
  reports it per receiver, at run time.
* A model answers `unknown command`, `{"status":"Failed"}`, or a
  refused connection. The driver lists the field in `unsupported` and
  stops asking for it until the next connect. A person can tell "this
  model does not have it" from "not read yet".

The existing `status.settledSettings` and `SettingsConfirmed` already
carry the Sent once tier. That is why the open problem about the WiiM
settings is resolved by this rule and not by a new mechanism. When
that problem was written, a field with no read would have been sent
again on every reconcile.

## One family vocabulary

A protocol block keeps its own vocabulary, because Denon and WiiM name
different things (plan 04). The families that mean the same thing take
the same name and the same field names in both blocks:

| Family | Denon | WiiM |
|---|---|---|
| `system` | power (reported), eco, dimmer, auto standby, speaker preset, audio input mode, video select, Bluetooth transmitter and output, and the unit's name if the receiver serves one | name, lights, buttons (today's `device`) |
| `tone` | control, bass, treble | bass, treble (`EQSet:Bass`, `EQSet:Treble`) |
| `eq` | the graphic EQ (today `audio.graphicEq`) | on or off, and the preset (`EQOn`, `EQOff`, `EQLoad`, `EQGetList`) |
| `audio` | DRC, LFE, effect, delays, subwoofer, restorer, headphone EQ, virtualizer, dialog enhancer | balance, the SPDIF delay |
| `speakers` | configuration, sizes, amp assignment, distances, crossovers, calibrated levels, subwoofer mode | output mode (`setAudioOutputHardwareMode`), subwoofer |
| `inputs` | names and visibility | none known |
| `presets` | quick select names | the stored presets (`getPresetInfo`), reported |
| `hdmi` | as today, plus the video controls | none: the WiiM's HDMI port is ARC in |
| `network` | none known | static address (`getStaticIpInfo`), reported |
| `bluetooth` | today's two `system` fields stay where they are | pairing history, reported |
| `audyssey`, `channelVolumes` | as today | none: Denon only |

So WiiM's `device` becomes `system`, and Denon's `audio.graphicEq`
becomes `eq.graphic`. The API is `v1alpha1`. The rename lands in one
release, and the release notes give the change for a declared spec.
The second zone's setup is a field set under `spec.zones.zone2`, the
same zone map that already carries its power, input, and volume.

## The protocols

Each field has one path. The driver takes the path that reads the field
back, and uses a second protocol only for a field the first cannot
reach.

### Denon

A Denon or Marantz receiver serves four interfaces, and none covers
every setting.

| Family | Control port (TCP 23) | HTTP, port 80 or 8080 | HTTPS setup, port 10443 |
|---|---|---|---|
| Speaker config and size | `SSSPC`, undocumented | none | where served |
| Amp assign | none | none | where served |
| Distances | `SSSDE`, undocumented | none | where served |
| Crossovers | `SSCFR`, undocumented | none | where served |
| Calibrated levels | `SSLEV`, undocumented, read | none | where served |
| Subwoofer mode | `SSSWM`, undocumented | none | where served |
| Input names and visibility | `SSFUN`, `SSSOD`, undocumented, read | `GetSourceRename`, `GetDeletedSource`, `SetSourceRename` on `AppCommand0300.xml` | |
| Quick select names | `SSQSNZMA`, undocumented | `GetQuickSelectName` | |
| Zone 2 setup | `Z2…`, documented | | |
| HDMI setup | `VSAUDIO`, documented; `SSHOS`, undocumented, read and set (measured) | | where served |
| Video controls | `VSMONI`, `VSASP`, `VSVPM` documented, not answered on the X1700H | remote-key codes on `/goform/formiPhoneAppDirect.xml` | |
| Zone power | `ZM`, `Z2`, `Z3` | `/goform/formiPhoneAppPower.xml?<zone>+PowerOn` | |

"Documented" means Denon's published control protocol; the operator
cites the CY2022 version. "Undocumented" means the commands the Home
Assistant `denonavr` library and other community code send.
`denon/AGENTS.md` records that the X1700H reads and sets the `SSHOS`
family, including HDMI Control, ARC, and Pow.Off Control. The first
draft of this plan said those had no read, which was wrong.
display-operator's open problem
[a receiver pulses the hotplug line for hours](../../display-operator/plans/open-problems/a-receiver-pulses-the-hotplug-line-for-hours.md)
needs the `hdmi.passThrough` value that `SSHOSPAS` reads.

The control port is the primary path. The HTTP interface is used for a
field the control port cannot reach. Its remote-key codes are presses,
not settings, so they have no read: `RCKSK0410826` turns HDMI Control
on and `RCKSK0410827` turns it off, and the video controls answer only
as presses. Each code is a Sent once field only where a read on another
path confirms it, as `SSHOSCON` confirms HDMI Control. A press that
toggles, with no read anywhere, stays out of the spec.

Port 10443 serves the receiver's HTTPS setup on some models. The
measured X1700H refuses connections there, so on that model amp assign
and the 10443 families are `unsupported`. The driver learns this from
the refused connection, not from the model name.

The receiver does not originate CEC. Denon's support states that the
AVR "does not generate a CEC command, but rather accepts it and
responds". So the receiver cannot wake the TV. Plan 09 built the wake
through a USB-CEC adapter on the cluster.

### WiiM

A WiiM serves one control interface, `httpapi.asp` over HTTPS, and the
UPnP services. `wiim/AGENTS.md` holds the commands and the authority of
each source. The new fields:

| Family | Setter | Read | Source |
|---|---|---|---|
| `eq` on, preset | `EQOn`, `EQOff`, `EQLoad:<name>` | `EQGetStat`, `EQGetList` | WiiM's document |
| `tone` | `EQSet:Bass`, `EQSet:Treble` | none named | `openapi.json` only |
| `speakers` output mode | `setAudioOutputHardwareMode:<n>` | `getNewAudioOutputHardwareMode` | WiiM's document |
| `speakers` subwoofer | the subwoofer commands | as named | `openapi.json` only |
| `audio` SPDIF delay | the SPDIF-delay command | as named | the extended source |
| `presets` | none | `getPresetInfo` | WiiM's document |
| `network` | none | `getStaticIpInfo` | WiiM's document |
| `bluetooth` | none | `getbthistory` | the extended source |

The two records disagree about the measured Amp. The open problem
said `EQGetStat` answers `{"status":"Failed"}` and the output mode
reads as `hardware: 7`, which is not the setter's enum.
`wiim/AGENTS.md` says neither read was tried on that firmware. The
first drill settles which is true. Either answer fits the rule: a read
that works makes the field Confirmed, and a read that fails makes the
WiiM's documented setter Sent once on that model.

### CEC

A receiver on a CEC bus is the bus's audio system. A `Receiver` gains a
`cec:` block that names the `CECBus`:

```yaml
spec:
  denon:
    address: den.example
  cec:
    bus: den
```

A `Receiver` holds at most one network block, `denon:` or `wiim:`, and
it may add `cec:`. It holds at least one block. The network block owns
every control it reaches. The `cec:` block owns what no network path
reaches, which starts with System Audio Mode: the request that makes
the TV send its sound to the receiver and pass its volume keys to it.
A `Receiver` with only `cec:` takes power, volume, and mute over CEC
as well. `status.cec` reports the receiver's logical address, physical
address, power, and System Audio Mode, from the `CECBus`'s view of the
bus. The node workload sends the request; the `Deployment` writes the
`Receiver`'s status. Plan 09 holds the bus design this block uses.

## Actions are not settings

A setting is a value the operator converges to. A command that does
something once is not a setting, and this plan does not model it:

* WiiM's three alarm slots are timers.
* WiiM's `reboot` works on a Mini and fails on an Amp.
* Denon's tuner and network player transport, and the HEOS controls.

The session asks (`volumeAsk`, `powerAsk`, `inputAsk`) are the only
path for an action today, and `media-operator` is their only writer.
An action from a person, such as a reboot, needs its own design and is
out of scope.

## Reading the receiver directly

A drill needs to read one raw setting, such as `SSSPC ?`, and the
control port accepts one connection, which the operator holds. The
first draft of this plan put a `query` command on the `Receiver`'s
commands topic. The operator connects to no message bus now (plan 77),
so that path is gone.

The proposal: the operator's metrics server gains a read-only endpoint
on the leader, `GET /receivers/<namespace>/<name>/query?line=<line>`.
It sends the line on the open connection and returns the receiver's
answer lines for 2 seconds. It refuses a Denon line that does not end
in `?`, and for a WiiM it accepts only a command named in the driver's
read table. So it never changes a setting. A person reaches it with
`kubectl port-forward`, the same way the capture endpoints of
display-operator and audio-operator are reached.

## Considered and set aside

* **A second control connection for diagnostics.** Whether the Denon
  receiver accepts it, refuses it, or drops the operator's connection
  is not settled, and the operator's connection carries the event
  stream.
* **A table of models and the families each supports.** The receiver
  answers or does not, and a table goes stale with each model year.
* **HTTP for everything on a Denon.** `AppCommand0300.xml` covers less
  than the control port, and its commands come from a packet capture of
  Denon's phone app.
* **Raw writes on any path.** A setting belongs in the spec, where it
  is declared, compared, and confirmed.
* **One shared settings schema for every protocol.** The protocols name
  different things. Shared family names give the consistency a person
  needs, and each block keeps the fields its receiver has.

## Phases

1. The rule in code: a tier on every row of each driver's family
   table, `status.<protocol>.unsupported`, and the `system` and `eq`
   renames. The query endpoint, because the later drills need it.
2. Denon reads: every control-port family above, into `status.denon`.
3. Denon writes: the families whose drill shows a read-back, in
   `speakers`, `inputs`, `presets`, and `zones.zone2`.
4. Denon HTTP and 10443: the HDMI Control press confirmed by `SSHOSCON`,
   and the 10443 families on a model that serves them.
5. WiiM: the families in the WiiM table, each at the tier its drill
   shows.
6. CEC: the `cec:` block and System Audio Mode.

## How it will be proved

On a home cluster with an AVR-X1700H and a WiiM Amp:

* `kubectl get receiver -o yaml` shows the Denon speaker configuration,
  distances, crossovers, calibrated levels, and subwoofer mode, and
  they match the receiver's setup menu.
* The query endpoint answers `SSSPC ?` with the receiver's lines, and
  refuses a line with no `?`.
* A changed distance in `spec.denon.settings.speakers` reaches the
  receiver, the menu shows it, and the `Receiver` reports it.
* `status.denon.unsupported` lists amp assign, because the X1700H
  refuses port 10443.
* On the WiiM Amp, `EQGetStat` and `getNewAudioOutputHardwareMode` are
  tried, and each field lands in the tier its answer gives. A declared
  `eq.preset` reaches the amp, and the app shows it.
* A `Receiver` with `cec:` on a bus with a TV puts the TV's sound on
  the receiver, and `status.cec` reports System Audio Mode on.
