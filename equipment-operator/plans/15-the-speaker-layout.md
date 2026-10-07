# 15, The speaker layout

Proposed on 2026-10-06. Replaces part of
[plan 13](rejected/13-every-setting-a-receiver-exposes.md): the speaker
setup, the subwoofer setup, and the read-only query endpoint. Not
built. No drill in this plan has run.

## The problem

During the speaker walk of audio-operator's
[plan 12](../../audio-operator/plans/completed/12-channel-layouts.md)
on a home cluster, the walk's side and back windows both played on the
surround speakers. The reason was in the receiver's on-screen menu: the
AVR-X1700H was set up as 5.1 with two height speakers, assigned as Top
Middle. A `Receiver` status that showed the speaker setup would have
answered the question in one `kubectl get`. Today it shows nothing of
the setup: the Denon driver reads six families, and none of them is the
speaker configuration, the distances, the crossovers, the calibrated
levels, or the subwoofer mode (`denon/protocol.go:29-36`).

A person also cannot change the setup without the receiver's menu.
Plan 14 makes the speaker layout a common family, `spec.settings.speakers`
and `spec.settings.subwoofer`, so one shape serves every brand. This
plan fills those families for the Denon.

## What the Denon answers

None of these commands is in Denon's published protocol. The FY21 sheet
for the AVR-X2700H to AVR-X6700H does not list them [S1], and neither
does the Marantz CY2022 sheet [S2]. They come from community code that
recorded live receivers. LaserGuruGuy/denon_avr marks several setters
"verified live" on an AVR-X3600H [S4]. The ranges come from the
AVR-X1700H manual, pages 205 to 215 [S3].

| Field | Read on port 23 | Answer | Range on the AVR-X1700H |
|---|---|---|---|
| size, per group | `SSSPC ?` | `SSSPC<group> LAR\|SMA\|NON`, one line per group [S4] | Large, Small, None [S3 p.206-210] |
| subwoofer count | `SSSPC ?` | `SSSPCSWF NON\|1SP\|2SP` [S4, S11] | Yes or No [S3 p.207] |
| surround back count | `SSSPC ?` | `SSSPCSBK 1SP\|2SP` [S4] | 1 or 2 speakers [S3 p.208] |
| distance, per channel | `SSSDE ?` | `SSSDE<channel> NNNN`, `SSSDESTP …`, `SSSDE END`; `0310` is 3.10 m [S4] | 0.00 to 18.00 m or 0.0 to 60.0 ft, step 0.1 or 0.01 m, 1 or 0.1 ft [S3 p.211] |
| calibrated level, per channel | `SSLEV ?` | `SSLEV<channel> NN[N]`, `SSLEV END`; 50 is 0 dB, half steps [S4, S5] | -12.0 to +12.0 dB [S3 p.212] |
| crossover | `SSCFR ?` | `SSCFR IDV\|ALL`, `SSCFRALL NNN`, `SSCFR<group> NNN`, `SSCFR END` [S4] | 40, 60, 80, 90, 100, 110, 120, 150, 180, 200, 250 Hz [S3 p.213] |
| subwoofer mode | `SSSWM ?`, or `SSSWO ?` on newer models | `SSSWM LFE\|L+M` [S4, S9] | LFE, LFE+Main [S3 p.214] |
| LFE low pass | `SSLFL ?` | `SSLFL NNN` [S4] | 80 to 250 Hz, default 120 [S3 p.214] |
| subwoofer level (trim) | `PSSWL ?` | `PSSWL ON\|OFF`, `PSSWL 50`, `PSSWL2 50`; 38 to 62, 50 is 0 dB [S1] | documented for the X2700H and S960H [S1] |

The groups are `FRO`, `CEN`, `SUA`, `SBK`, `SWF`, `FRH`, `FRD`, `SUD`,
`TFR`, `TPM`, `TPR`, `RHE`, and `BKD`. Each maps to a pair or a single
channel, such as `TPM` to `TML` and `TMR` [S4]. The channel codes are the
ones `CV` already uses, and they match plan 14's channel enum, except
that the Denon names the first subwoofer `SW`, which maps to `SW1`.

The receiver ties some fields together. With Subwoofer set to No, Front
becomes Large. With Front set to Small, Subwoofer becomes Yes. With
Surround set to None, Surround Back and Surround Dolby become None.
A crossover is settable only for a Small speaker, or for every speaker
when the subwoofer mode is LFE+Main [S3 p.206-214].

The speaker preset holds the layout. Each of presets 1 and 2 stores the
Amp Assign, the speaker configuration, the distances, the levels, and
the crossovers [S3 p.215]. So every field in this plan belongs to the
active preset, which `SPPR ?` reports.

`channelVolumes` is not the calibrated level. `CV` is the Denon's
Channel Level Adjust, a trim the receiver keeps for each input source
on top of the calibrated levels [S3 p.212]. The measured AVR-X1700H
reports `CV` only for the channels the current sound mode plays: in
`MSSTEREO` it sent `CVFL`, `CVFR`, and `CVSW` and no center
(`denon/testdata/avr-x1700h.txt:35-38`). `channelVolumes` stays in
`spec.denon.settings`, and `SSLEV` fills the common `level`.

## The design

### Status in the common vocabulary

The Denon driver adds the seven queries above to its connect queries,
and folds each answer into `status.settings.speakers` and
`status.settings.subwoofer`, in plan 14's shape:

```yaml
status:
  settings:
    speakers:
    - {channel: FL, size: Small, distance: 3.15m, level: 0dB, crossover: 80Hz}
    - {channel: FR, size: Small, distance: 3.10m, level: 0dB, crossover: 80Hz}
    - {channel: C, size: Small, distance: 2.90m, level: 1.5dB, crossover: 80Hz}
    - {channel: SL, size: Small, distance: 2.10m, level: -1dB, crossover: 90Hz}
    - {channel: SR, size: Small, distance: 2.20m, level: -1dB, crossover: 90Hz}
    - {channel: TML, size: Small, distance: 2.40m, level: 0.5dB, crossover: 120Hz}
    - {channel: TMR, size: Small, distance: 2.40m, level: 0.5dB, crossover: 120Hz}
    - {channel: SW1, distance: 3.60m, level: -3dB}
    subwoofer: {enabled: true, mode: LFE, lowPass: 120Hz, level: 0dB}
  denon:
    ampAssign: topMiddle
```

Each value is in the unit the receiver is set to: `SSSDESTP` names
meters or feet, and the distance carries that unit. A group's size
fills each of its channels. A channel whose group answers `NON` is not
listed. A Large speaker under subwoofer mode LFE has no crossover, and
the entry leaves `crossover` out. With `SSCFR ALL`, every entry that
takes a crossover shows the `SSCFRALL` value. A subwoofer entry has no
size; `SSSPCSWF 2SP` lists `SW1` and `SW2`.

`subwoofer.enabled` comes from `PSSWR`, which the driver reads today
and which plan 14 hoists. `subwoofer.level` is `PSSWL`, the audio
menu's subwoofer trim on top of the calibration. It is not the
calibrated `SW1` level in `speakers`. The WiiM's `getSubLPF` `level`
is the same kind of trim, so the two share the common field.

The answers end with an `END` line. The driver writes a family to
status only after its `END` line, so a status never shows half a
layout.

### The query endpoint

A drill needs to read one raw setting, and the control port accepts one
connection, which the operator holds. The operator's metrics server
gains two read-only endpoints on the leader, reached with
`kubectl port-forward`, the way the capture endpoints of
display-operator and audio-operator are reached:

* `GET /receivers/<namespace>/<name>/query?line=<line>` sends the line
  on the open connection and returns the lines whose prefix matches the
  line's stem for 2 seconds. The Denon reports events and answers on
  one stream, so the prefix decides what belongs to the answer [S7].
* `GET /receivers/<namespace>/<name>/lines?prefix=<prefix>&for=<seconds>`
  sends nothing, and returns every line with the prefix that arrives in
  the window, at most 120 seconds. Drill D2 needs it to see the lines
  the receiver sends by itself.

A driver serves these only if it implements an optional interface in
`equipment/`:

```go
// Querier reads raw lines from a device for a drill. It never changes
// a setting.
type Querier interface {
    Query(ctx context.Context, line string, wait time.Duration) ([]string, error)
    Lines(ctx context.Context, prefix string, wait time.Duration) ([]string, error)
}
```

The `equipment.Driver` interface does not change. The WiiM driver does
not implement `Querier` yet, so the endpoints answer 404 for a WiiM.

The Denon's `Query` refuses a line that does not end in `?`. It also
refuses a line longer than 24 characters, a character outside
`A-Z 0-9 : . / + ?`, and the prefixes `SY` and `RC`, because `SYRST`
resets the receiver and an `RC` line is a remote key press [S1]. A
query with no answer returns an empty list, which is the Denon's only
way to say it lacks a command.

### Declaring the layout

Phase 2 makes the fields declarable through plan 14's settings engine.

**Snapping.** A distance and a level round to the receiver's step: the
distance to the step `SSSDESTP` reports, the level to 0.5 dB. A
crossover and a low pass are enumerated, so a value the model does not
take is Rejected, and the exception names the values it takes. A
declared size or crossover for one channel of a pair must match the
other channel, because the Denon sets them per group. A pair that
disagrees is Rejected.

**The speaker preset binding.** The declared layout belongs to the
preset that `spec.denon.settings.system.speakerPreset` names, and to
preset 1 when none is declared. While another preset is active, every
declared speaker and subwoofer field is Blocked, with the reason
"speaker preset 2 is active; the declared layout is preset 1". The
operator switches the preset only when the preset is declared, and it
never changes an undeclared field to make a declared one land.

**The preset clears the stored values.** When an `SPPR` line reports a
new preset, the driver clears its speaker and subwoofer state, and asks
the seven queries again. Until the `END` lines arrive, the fields are
unreported, so the engine does not compare a declared value with the
old preset's values and send the difference.

**The send order.** The receiver changes dependent fields by itself, so
the engine sends the speaker family in this order: subwoofer count,
front, center, surround, surround back, the height and Dolby groups,
subwoofer mode, low pass, crossover selection, crossovers, then
distances. A declared combination the receiver coerces, such as Front
Small with Subwoofer None, is not reported back at the declared value.
It uses up the 3-send budget, and `settingExceptions` names it.

**The levels.** No source shows a setter for `SSLEV` on port 23 [S4].
The level stays Reported until drill D3 shows that `SSLEV<channel> NN`
reads back. A declared level before then is listed with the reason that
the protocol has no row for it.

**Amp assign.** Amp Assign decides which groups exist: on the
AVR-X1700H, Top Middle replaces Surround Back [S3 p.205]. It is a
Denon brand field, `status.denon.ampAssign`, Reported. Port 23 has no
known read. Plan 17's second paths reach it: port 10443 `speakers`
type 2, and port 1256 `GET_AVRSTS`. A declared speaker in a group the
Amp Assign does not provide is Blocked, with the Amp Assign in the
reason.

**Calibration.** Running Audyssey rewrites the sizes, the distances,
the levels, and the crossovers. A declared layout then differs from
the receiver, and the operator sends the declared values back over the
calibration. The guide says so: declare the layout from status after
calibrating, and remove the declared layout before calibrating again.

The WiiM's subwoofer rows belong to
[plan 16](16-the-wiims-settings.md).

## Considered and set aside

* **A layout keyed by preset.** `speakers` under each of presets 1 and
  2 would let a person declare both. To write the inactive preset the
  operator would switch the preset, which changes the sound in the
  room, and switch it back.
* **Folding `channelVolumes` into `level`.** `CV` is a per-input trim
  and `SSLEV` is the calibrated level. They are different settings,
  and the menu shows them in different places [S3 p.130, p.212].
* **A timer that reads the layout again.** The layout changes when a
  person uses the setup menu or the preset changes. Drill D2 shows
  which lines the receiver sends then, and plan 17 reads again on those
  events.
* **Port 1256 as the layout path.** `GET_AVRSTS` returns the
  configuration in one JSON answer [S9], but its writes run inside the
  Audyssey calibration mode. Port 23 reads and sets the same fields.
* **A table of models.** The answers to the seven queries say what the
  model has.

## Phases

1. Buildable now, before plan 14. The `Querier` interface and the two
   endpoints for the Denon. The seven queries in `Queries`, the
   parsers, and `status.settings.speakers` and
   `status.settings.subwoofer`, written only after each `END` line. The
   seven queries asked again after an `SPPR` line, so status follows
   the active preset. Drills D1, D2, D4, and D5, with their answers
   recorded in `denon/testdata/avr-x1700h.txt`.
2. After plan 14 and drill D3. The speaker and subwoofer rows in the
   Denon's field table, at the tier each drill showed. Snapping, the
   preset binding, the cleared state, the send order, the Amp Assign
   blocking, and the calibration note in the guide.

## Drills

None has run. Each runs through the query endpoints on a home cluster
with the AVR-X1700H.

* **D1, the reads.** With the main zone on, and again in standby: `SSSPC ?`,
  `SSSDE ?`, `SSLEV ?`, `SSCFR ?`, `SSSWM ?`, `SSSWO ?`, `SSLFL ?`,
  `PSSWL ?`, and `SPPR ?`. Record each answer, and which queries are
  silent in standby.
* **D2, the lines the receiver sends by itself.** Watch the `SS`, `PS`,
  and `MN` prefixes for 120 seconds while a person changes a size, a
  distance, and a level in the on-screen menu, and closes the menu.
* **D3, the setters.** Phase 2 needs it. For each of `SSSPC<group>`,
  `SSSDE<channel>`, `SSLEV<channel>`, `SSCFR<group>`, `SSCFRALL`,
  `SSSWM`, and `SSLFL`: send a value that differs, record whether the
  receiver echoes it, query the family, and send the old value back.
* **D4, the distance encoding.** Read `SSSDESTP` and the `SSSDE` values
  with the unit set to meters, then to feet, at both steps.
* **D5, the preset.** Send `SPPR 2`. Record whether the receiver sends
  the `SS` families again by itself, or whether the queries must be
  asked. Send `SPPR 1`.

## How it will be proved

On a home cluster with an AVR-X1700H:

* `kubectl get receiver -o yaml` shows the layout, the subwoofer mode,
  and the low pass, and they match the receiver's setup menu, including
  the two Top Middle speakers.
* The query endpoint answers `SSSPC ?` with the receiver's lines, and
  refuses `SSSPC FRO SMA` and `SYRST?`.
* After `SPPR 2` at the receiver, status shows preset 2's layout.
* In phase 2, a changed distance in `spec.settings.speakers` reaches
  the receiver, the menu shows it, and the status reports it. A
  declared crossover of 85 Hz is Rejected, and the exception lists the
  values the receiver takes.

## Sources

* [S1] Denon FY21 AVR control protocol V02:
  https://github.com/mkulesh/onpc/blob/master/doc/Denon/FY21_AVR_DENON_PROTOCOL_V02_04062020.xlsx
* [S2] Marantz FY23 (CY2022) AVR control protocol V05:
  https://github.com/burger-mtbkr/av10-dashboard/blob/main/marantz/Marantz_FY23-CY2022_AV_CINEMA_PROTOCOL_V05.xlsx
* [S3] AVR-X1700H owner's manual:
  https://cdn.teufelaudio.com/products/DENON/DENON%20AVR%20X1700H%20DAB/bda-denon_avr_x1700h_dab-en
* [S4] LaserGuruGuy/denon_avr, `protocol_profile.json` and `parser.py`:
  https://github.com/LaserGuruGuy/denon_avr/tree/main/custom_components/denon_avr/avr
* [S5] mkulesh/onpc, port 23 and XML captures from an AVR-X1500H:
  https://github.com/mkulesh/onpc/tree/master/doc/Denon/Samples
* [S7] plotdot/immersive, `docs/api-notes.md`:
  https://github.com/plotdot/immersive/blob/main/docs/api-notes.md
* [S9] srinivas486/audyssey-rew-tuner, `COMMAND_INVENTORY.md`:
  https://github.com/srinivas486/audyssey-rew-tuner/blob/main/COMMAND_INVENTORY.md
* [S11] Wolbolar/IPSymconDenon, `DenonClass.php`:
  https://github.com/Wolbolar/IPSymconDenon/blob/master/DenonClass.php
