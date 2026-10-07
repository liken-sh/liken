# 14, One settings model for every receiver

Proposed on 2026-10-06. Replaces part of plan 13,
[every setting a receiver exposes](rejected/13-every-setting-a-receiver-exposes.md):
its rule, its tiers, its family vocabulary, and its `unsupported`
list. Plans 15 to 18 take the rest. Not built. This plan is the
breaking release of the `Receiver` settings API.

## The problem

A person with a Denon and a WiiM sets the same knob in two shapes. The
bass is `spec.denon.settings.tone.bass` on one and has no field on the
other. The unit's name is `spec.wiim.settings.device.name` on the WiiM
alone. Each later brand would add a third shape, and a person who
changes receivers would rewrite the spec.

The code repeats itself too. `setSettings` and `setWiimSettings` are
near copies (`reconcile.go:413-437`, `485-505`), and each driver
writes its own `ConfirmedBy` and `Pending` by hand
(`denon/settings.go:65-157`, `wiim/settings.go:63-116`). The send
budget (`send_budget.go:170-205`) and `status.settledSettings`
(`settled.go:75`) already work on any block.

The tiers exist in the code, but nothing names them. A field the
receiver reports is driven back; a field it does not report is sent
once for each change of the block (`denon/settings_pending.go:29-38`).
A person cannot see which treatment a field gets, or tell "this model
does not have it" from "not read yet". The WiiM driver also reports a
zero for a failed read (`wiim/settings.go:154-162`);
[plan 16](16-the-wiims-settings.md) fixes that first.

Plan 13's rule, "a `Receiver` exposes every setting the receiver
serves on any protocol", grows without bound. A Yamaha or an Arcam
serves more than 100 settings, and each needs a codec, a CRD field,
and a drill.

## The rule

Every field the operator builds follows the tier rules below and lives
in one of two places. A setting that means the same thing on every
brand is common, in `spec.settings`. A setting one brand alone has is
in that brand's block. Each plan names the families it builds, and
`status.settingExceptions` shows the gaps on each unit.

## The design

### Common settings and brand settings

The protocol block stays, because the connection differs by brand,
and so do the features no other brand shares. Exactly one network
block is required, as the CEL rule `exists_one` requires today
(`deploy/receivers-crd.yaml:80`). `cec:`
([plan 18](18-system-audio-mode-on-the-wake.md)) is not a network
block and may sit beside one.

```yaml
spec:
  denon:
    address: receiver.example
    settings:                     # Denon's own words
      system: {eco: auto, speakerPreset: 1}
      audyssey: {multeq: reference, dynamicEq: true}
      toneControl: true
      channelVolumes: {...}       # the CV trims, not calibrated levels
  settings:                       # every brand; the main zone and the unit
    system: {name: Theater, autoStandby: 30m}
    tone: {bass: 2dB, treble: 0dB}
    audio: {balance: 0}
    eq: {enabled: true}
    hdmi: {control: true, arc: true, passThrough: true}
    subwoofer: {enabled: true, mode: LFE, lowPass: 120Hz}
    speakers:
    - {channel: FL, size: Small, distance: 3.15m, level: 0dB, crossover: 80Hz}
    - {channel: SW1, distance: 3.60m, level: -3dB}
    inputs:
    - {name: MPLAY, label: Media Player}
    - {name: PHONO, hidden: true}
```

`spec.zones` does not change. Per-zone setup later takes the same
common types under `spec.zones.<zone>.settings`; no plan builds it
now. `spec.inputs` does not change either: it routes an input to a
machine and a monitor. Labels and hidden inputs go in
`spec.settings.inputs`, because a hidden input such as `PHONO` has no
machine, and `spec.inputs` requires `machine` and `monitor`.

Status mirrors spec: `status.settings` holds the common values the
receiver reported, and `status.denon` and `status.wiim` keep the brand
values and the WiiM's device snapshot, without the hoisted fields.

### The families and their units

A US receiver shows distances in feet, and a person copies values off
the receiver's menu. The API conventions also ask for no floats in
spec. So a quantity is a string with a unit, and a CEL pattern
validates it: a distance `3.15m` or `10.3ft`, a level or tone `-1.5dB`,
a frequency `80Hz`, a duration `30m` or `Off`. Status reports each
value in the unit the receiver is set to. The balance stays a number
from -1.0 to 1.0, as `wiim/settings.go:121-126` takes it today.

| Family | Fields | Built by |
|---|---|---|
| `system` | `name`, `autoStandby` | this plan |
| `tone` | `bass`, `treble` | this plan |
| `audio` | `balance` | this plan |
| `eq` | `enabled` | this plan |
| `hdmi` | `control`, `arc`, `passThrough` | this plan |
| `subwoofer` | `enabled`; `mode`, `lowPass`, `level` (a trim on top of the calibration) | this plan; [plan 15](15-the-speaker-layout.md) |
| `speakers` | list keyed by `channel`: `size`, `distance`, `level`, `crossover` | [plan 15](15-the-speaker-layout.md) |
| `inputs` | list keyed by `name`: `label`, `hidden` | [plan 17](17-the-denons-inputs-zones-and-web-setup.md) |

The channels are one enum in `equipment/`, the Dolby speaker positions
in the abbreviations Denon and Audyssey use: `FL FR C SW1 SW2 SL SR SB
SBL SBR FHL FHR FWL FWR TFL TFR TML TMR TRL TRR RHL RHR FDL FDR SDL SDR
BDL BDR SHL SHR TS CH`. Yamaha's `FPL`/`FPR` map to `FHL`/`FHR`, and
`RPL`/`RPR` to `RHL`/`RHR`. A size is `Small`, `Large`, or `None`.
`speakers` and `inputs` are lists keyed by name, because the API
conventions allow no maps of subobjects
([API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md),
"Lists of named subobjects preferred over maps"). An absent speaker
entry is not managed, and only `size: None` removes a group. A
protocol that sets size or crossover for a pair rejects a pair whose
two entries disagree.

### The moves in the breaking release

| Today | After |
|---|---|
| `spec.denon.settings.tone.bass`, `.treble` (integer dB) | `spec.settings.tone.bass`, `.treble` (dB strings) |
| `spec.denon.settings.tone.control` | `spec.denon.settings.toneControl` |
| `spec.denon.settings.hdmi.control`, `.arc`, `.passThrough` | `spec.settings.hdmi.*` |
| `spec.denon.settings.audio.subwoofer` | `spec.settings.subwoofer.enabled` |
| `spec.denon.settings.audio.graphicEq` (on or off) | `spec.settings.eq.enabled` |
| `spec.denon.settings.system.autoStandby` | `spec.settings.system.autoStandby` |
| `spec.wiim.settings.device.name` | `spec.settings.system.name` |
| `spec.wiim.settings.audio.balance` | `spec.settings.audio.balance` |

Every other brand field keeps its name. The API is `v1alpha1`, so the
moves land in one release with no conversion webhook. The API server
prunes a field the schema no longer declares when it reads a stored
object
([Controlling pruning](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#controlling-pruning)).
After the upgrade the operator stops managing a moved field until a
person declares it at its new path, and the receiver keeps its value.
The release notes give each move.

Not hoisted: the dimmer, whose values differ by brand; DRC, the dialog
enhancer, the Audyssey modes, and the sound modes, which each brand
names differently; the two Denon delays, because which one is lip sync
is ambiguous; WiiM's LED and buttons, whose nearest Denon
counterparts, the dimmer and the panel lock, take other values; and
the receiver's own volume limit, an open question below.

### The tiers

| Tier | Meaning | How the operator treats it |
|---|---|---|
| Confirmed | A documented read and a setter. | Compares, sends a field that differs, at most 3 times at one value. |
| Sent once | A documented setter with no read. | Sends it once for each change of its family. |
| Reported | A read with no setter. | Status only; no spec field. |
| Unsupported | This model does not have it. | Not sent; listed in `status.settingExceptions`. |

The tier in a driver's table is a ceiling. On one unit, a Confirmed
field the unit never reads back is treated as Sent once, which is
today's behavior, now shown in status. An undocumented setter is
declarable only after a drill shows the receiver reads it back.

### Three answer kinds

Six of the protocols surveyed below have a code for "valid, but not
in this state", such as a zone in standby. A driver that listed such
a field as Unsupported would stop sending a field the model has. So
each driver maps its device's answers to three kinds:

* **Refused.** The command does not exist on this model: WiiM
  `unknown command`, YNCA `@UNDEFINED`, Arcam `0x83`, PJLink `ERR1`.
  The field is Unsupported until the next connect.
* **Not now.** Valid, but not in this state: YNCA `@RESTRICTED`, Arcam
  `0x85`, Anthem `!E`, PJLink `ERR3`. It counts against the 3-send
  budget and never makes a field Unsupported.
* **Silent.** A Denon sends no error line, and the driver ignores a
  line it does not know (`denon/state.go:76`). A query with no answer
  by the end of the first read makes the field Unsupported, with a
  reason that says so. The driver asks again after an event that
  changes the setup, such as a speaker preset change or the setup
  menu closing.

WiiM's `{"status":"Failed"}` is mapped for each row, because some
reads answer it when the value is empty, such as no renamed inputs.

A probe on a silent protocol costs the whole first read, so a driver
reads the model's capability document before
it probes, where one exists: the `display` attribute on Denon port
10443, the WiiM's sound-card list, Yamaha MusicCast `getFeatures`,
Sony `getSupportedApiInfo`, and Onkyo `NRI`. A field the document
leaves out is Unsupported, and the reason names the document.

### Status lists the exceptions

A tier for each field in status would repeat the driver's table on
every `Receiver` and bury the fields that need attention. The table is
the same for every unit of a protocol, so the tier goes in the CRD
field description and the reference. Status lists only the deviations,
in one list keyed by `path`:

```yaml
status:
  settingExceptions:
  - path: settings.speakers[FL].crossover
    state: Rejected
    reason: "85Hz: this receiver takes 40, 60, 80, 90, 100, 110, 120, 150, 180, 200, 250Hz"
  - path: denon.settings.video.monitor
    state: Unsupported
    reason: "no answer to VSMONI ? during the first read"
  - path: settings.hdmi.arc
    state: SentOnce
    reason: "the receiver did not read it back"
```

* **Unsupported** comes from a Refused answer, a Silent query, or a
  capability document. **SentOnce** is a Confirmed row this unit does
  not read back.
* **Rejected**: the declared value is not one this model takes.
  Continuous quantities (distance, level, tone) round to the model's
  step and are not rejected. Enumerated ones (crossover, size, standby
  interval) must match one of the model's values.
* **Blocked**: a prerequisite the person controls is not met. The
  operator never changes an undeclared field to make a declared one
  land.

A field the protocol has no row for is listed only when it is
declared ("the wiim protocol has no speaker layout"). A reason holds
the device's own text where there is one, as `AGENTS.md` asks of every
error. A declared field in the list makes `SettingsConfirmed` False
(`status.go:53-65`), and the message names the paths. The list is
`x-kubernetes-list-type: map` with a `maxItems`, so a CEL rule on it
stays inside the cost budget.

### The generic engine

One engine replaces `setSettings` and `setWiimSettings`, so a new
driver writes a table and wire codecs, not reconcile logic. The
`Driver` interface does not change; settings get their own interface
in `equipment/`:

```go
type Field struct {
	Path string // "settings.tone.bass", "denon.settings.eco"
	Tier Tier   // Confirmed, SentOnce, or Reported
}

type Settings interface {
	Fields() []Field
	Reported() (common CommonSettings, brand any) // nil = not read
	Exceptions() []Exception
	Apply(ctx context.Context, common CommonSettings, brand any) []Exception
}
```

The engine works on JSON leaves, as `spend` does today. A declared
leaf with an Unsupported, Rejected, or Blocked exception is not sent.
A Confirmed leaf the unit reports is sent when it differs. A Confirmed
leaf not reported after the first read is sent once and listed as
`SentOnce`. A Sent once leaf is compared with what the operator sent
before, and with its family's digest after a restart. `ConfirmedBy`
becomes one compare of declared and reported leaves, and the deep copy
becomes a JSON round trip. A driver keeps its types, its table, its
codecs, and its mapping of answers to the three kinds.

### One digest per family

`status.settledSettings` holds one digest for each block today
(`settled.go:27-31`), so after a restart an edit to one field resends
every unread field of the block. The engine keys the digest by family
instead, such as `spec.settings.tone` and
`spec.denon.settings.audyssey`. A status that holds the old block
digests is read as "every family of that block settled", the way
`newSettledRecord` adopts the older `settingsGeneration`
(`settled.go:46-57`).

### The CRD stays typed, and a test holds it to the tables

A typed schema gives `kubectl explain` and validation at apply. The
CRD is written by hand (`deploy/receivers-crd.yaml`, 53,116 bytes),
no test compares it with the Go types, and plans 15 to 17 add rows by
the hundred. So a test walks the spec schema and every driver's
table. It fails when a spec
property has no row, a row has no property, or a field description
does not name the row's tier. `TestTheAPIServerWouldAcceptTheCRD`
(`receiver_test.go:186`) keeps checking the CEL cost.

## Adding a protocol block

1. A package `<protocol>/` with its brand settings type, its table of
   `Field` rows, its codecs, and its answer mapping.
2. A capability-document read before any probe, where one exists.
3. A fake peer on `net.Pipe`, or an `http.Handler` on an in-memory
   transport, that answers from a real device's transcript in
   `testdata/` (the `testing` skill; `denon/testdata/avr-x1700h.txt`).
4. The block in the CRD, the `exists_one` rule, `startDriver`
   (`reconcile.go:915-935`), and `declaredBlocks` (`settled.go:61`).
5. A row for each common field the protocol reaches, at the tier its
   documents give.

One table of cases runs against each driver's fake in a `synctest`
bubble. It proves that a Confirmed field reported at another value is
sent and stops after 3 sends; a Confirmed field the fake never reads
is sent once and listed `SentOnce`; a Sent once field is not resent
after a restart when its digest matches; a Refused answer lists the
field Unsupported with the device's text, and a reconnect clears it; a
Not now answer counts a send and lists nothing; a Silent query lists
the field after the first read; and a declared common field with no
row is listed with the protocol's name.

### The protocols surveyed

A survey on 2026-10-06 read the public control documents of the
receivers a home theater may hold. "Unverified" marks what it could
not confirm.

| Protocol | Push | Setter read-back | Refused | Not now | Capability document | Common families | Cannot map |
|---|---|---|---|---|---|---|---|
| Yamaha YNCA, TCP 50000 | yes | usually | `@UNDEFINED` | `@RESTRICTED` | weak | system, tone, eq, hdmi, subwoofer, speakers | YPAO stays brand; scenes are not settings |
| Yamaha MusicCast, HTTP | UDP, renewed every 10 min | as an event (unverified) | code 3, 4 | code 5 | `getFeatures` | as YNCA | as YNCA |
| Onkyo eISCP, TCP 60128 | yes | echoed | `N/A` | none named | `NRIQSTN` | tone, speakers levels, hdmi, system | AccuEQ, Dirac stay brand |
| Sony Audio Control API | WebSocket | unverified | 12, 15 | 7 | `getSupportedApiInfo` | eq, speakers, subwoofer | sound field, night mode |
| Anthem, TCP 14999 | yes | yes | `!I` | `!E`, `!Z` | none | system, hdmi | setup held per input |
| Arcam, TCP 50000 | yes | in the answer | `0x83`, `0x84` | `0x85` | none | tone, audio, speakers, hdmi, subwoofer | room EQ stays brand |
| Sonos UPnP, HTTP 1400 | GENA | event only | SOAP fault (unverified) | unverified | device XML | subwoofer (`SubGain`) | tone: -10 to 10 with no unit stated |
| HEOS CLI, TCP 1255 | after registration | echoes request | `eid=1`, `15` | unverified | none | none | network player only |
| PJLink, TCP 4352 | Class 2, UDP | `OK` only | `ERR1` | `ERR3` | `CLSS ?`, `INST ?` | none | a projector |

Sonos documents bass and treble as -10 to 10 and states no unit
([RenderingControl](https://sonos.svrooij.io/services/rendering-control)),
so `tone.bass` in dB needs a stated step-to-dB rule or a brand field.

### References for the next protocol block

A vendor document states the protocol, and a maintained client library
shows how real units answer it. Where a vendor publishes no document,
the library is the only source, and its setters need a drill before
they are declarable. Each link resolved on 2026-10-06.

| Protocol | Vendor document | Client code |
|---|---|---|
| Denon and Marantz | `denon/AGENTS.md` lists the protocol documents | [`denonavr`](https://github.com/ol-iver/denonavr); plans 15 and 17 cite the rest |
| WiiM and LinkPlay | `wiim/AGENTS.md` lists the API documents | [`pywiim`](https://github.com/mjcumming/pywiim) |
| Yamaha YNCA | none public | [`ynca` protocol notes](https://raw.githubusercontent.com/mvdwetering/ynca/master/docs/PROTOCOL.md) |
| Yamaha MusicCast | none public | [`aiomusiccast`](https://raw.githubusercontent.com/vigonotion/aiomusiccast/master/aiomusiccast/pyamaha.py) |
| Onkyo, Integra, Pioneer eISCP | [ISCP command list](https://community.symcon.de/uploads/short-url/7mxbIQ7qRIghfbEQrvcrEkU57ad.pdf) | [`onkyo-eiscp`](https://github.com/miracle2k/onkyo-eiscp), [openHAB binding](https://raw.githubusercontent.com/openhab/openhab-addons/main/bundles/org.openhab.binding.onkyo/src/main/java/org/openhab/binding/onkyo/internal/handler/OnkyoHandler.java) |
| Sony Audio Control API | none found | [`python-songpal`](https://github.com/rytilahti/python-songpal) |
| Anthem | none fetched | [`python-anthemav`](https://raw.githubusercontent.com/nugget/python-anthemav/master/anthemav/protocol.py) |
| Arcam | [RS232 and IP protocol, AV40 family](https://WWW.ARCAM.CO.UK/ugc/tor/AV41/Custom%20Installation%20Notes/RS232_5_10_20_30_40_11_21_31_41__SH289E_F_07Oct21.pdf) | none surveyed |
| NAD, serial and TCP 23 | [T 163, T 763, T 773 protocol](https://applicationmarket.crestron.com/content/Help/NAD/t163_t763_t773.pdf) | none surveyed |
| Bluesound and BluOS, HTTP 11000 | none public | [`pyblu`](https://raw.githubusercontent.com/LouisChrist/pyblu/main/src/pyblu/player.py) |
| Sonos UPnP | none; community reference [sonos.svrooij.io](https://sonos.svrooij.io/services/rendering-control) | the same site documents each service |
| Sonos local control | [Sonos control API](https://docs.sonos.com/docs/control) | none surveyed |
| HEOS CLI | [HEOS CLI 1.10](https://cdn.hackaday.io/files/1869467998297664/HEOS_CLI_ProtocolSpecification.pdf), sections 4.1.1 and 6.2 | none surveyed |
| PJLink | [PJLink 2.10](https://pjlink.jbmia.or.jp/english/data_cl2/PJLink_5-1.pdf) | none surveyed |

The survey left NAD and BluOS out of the table above, because most of
their answers were unverified. Their references stay here for the
block that needs them.

## Considered and set aside

* **One shared spec with no protocol blocks.** The connection differs
  by brand, a UUID for a WiiM and an address for a Denon, and each
  brand has features no other has.
* **One CRD for each protocol.** A `Player` names one `Receiver`, and
  `media-operator` reads one status shape. A kind for each brand
  splits every consumer, and the common settings still need one type.
* **An opaque settings blob, as in DRA.** DRA passes driver parameters
  as a `RawExtension` of at most 10 Ki
  ([ResourceClaim v1beta1](https://v1-32.docs.kubernetes.io/docs/reference/kubernetes-api/workload-resources/resource-claim-v1beta1)).
  That gives no `kubectl explain` and no validation at apply.
* **CEL rules that reject a common field for a protocol.** Support
  differs by model, not only by protocol, so a rule would be wrong for
  some models. The model answers at run time.
* **Renaming the brand families.** A brand block holds only what the
  brand alone has, so its own words, such as `audyssey`, are clearest.
* **PJLink as a block.** A projector belongs to the `Projector` kind
  that `00-design.md` leaves for later.
* **A `heos:` block beside `denon:`.** HEOS adds no setting, and two
  network blocks would need a rule for which one owns volume.

## Phases

The phases build the engine and the moves for today's fields only.
New families come with [plan 15](15-the-speaker-layout.md),
[plan 16](16-the-wiims-settings.md), and
[plan 17](17-the-denons-inputs-zones-and-web-setup.md).

1. The `equipment.Settings` interface, table rows with tiers for every
   field the drivers build today, and the engine in place of
   `setSettings` and `setWiimSettings`.
2. The answer kinds in both drivers, and `status.settingExceptions`
   feeding `SettingsConfirmed`.
3. One digest for each family, with the adoption of block digests.
4. `spec.settings` and `status.settings` with the moves, the unit
   patterns, and the channel enum.
5. The CRD-to-table test.

## How it will be proved

On a home cluster with an AVR-X1700H and a WiiM Amp. None of these
drills has run.

* A `Receiver` that declared `spec.denon.settings.tone.bass` before
  the upgrade keeps its bass, and the operator sends nothing for it
  until `spec.settings.tone.bass` is declared.
* `spec.settings.tone.bass: 2dB` reaches the AVR-X1700H, its menu
  shows +2dB, and `status.settings.tone.bass` reports `2dB`.
* `spec.settings.system.name` renames the WiiM Amp, and the WiiM Home
  app shows the name.
* `spec.settings.tone.bass` on the WiiM Amp is listed in
  `status.settingExceptions` with a reason that names the wiim
  protocol, and `SettingsConfirmed` is False.
* After an operator restart, an edit to `spec.settings.hdmi` sends the
  HDMI family alone, and the log names no other family.

## Open questions

* **The receiver's own volume limit.** Denon `SSVCTZMALIM` and WiiM
  `setMaxVolume` cap the volume on the unit, which also stops the
  receiver's own remote. Does one back `spec.volume.max`, which today
  caps a session alone, or does it stay a brand field?
* **The Denon tone-control prerequisite.** A Denon takes bass and
  treble only while tone control is on. A declared
  `spec.settings.tone.bass` with `spec.denon.settings.toneControl`
  absent or false is then Blocked. Should a declared common bass imply
  `toneControl: true` on a Denon instead?
* **Whether `SentOnce` resets on reconnect.** A unit that answers a
  read late, such as one waking from standby, would otherwise stay
  listed until the operator restarts.
