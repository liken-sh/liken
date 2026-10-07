# 17, The Denon's inputs, zones, and web setup

Proposed on 2026-10-06. Replaces part of
[plan 13](rejected/13-every-setting-a-receiver-exposes.md): the Denon's
input names, quick select names, second zone setup, video controls, and
its HTTP and HTTPS interfaces. Not built. No drill in this plan has
run.

## The problem

A Denon or Marantz receiver keeps much of its setup where the
`Receiver` cannot see it. The input names and the hidden inputs, the
quick select names, the second zone's setup, the picture controls, the
volume limit, and the input assignment are each read or set only at the
receiver's menu today. Some of them are on the control port under
commands Denon does not document. Others are only on the receiver's
HTTP and HTTPS interfaces, which the driver does not use. Amp Assign,
which [plan 15](15-the-speaker-layout.md) needs to explain the speaker
layout, has no known read on port 23 at all.

A receiver serves four interfaces besides port 23, and none covers
every setting:

| Interface | What it serves | Source |
|---|---|---|
| HTTP, port 8080 or 80, `/goform/` | `formiPhoneAppDirect.xml?<line>` passes a port-23 line through HTTP. `AppCommand.xml` and `AppCommand0300.xml` take an XML `POST`. | [S6] `const.py:609-635`, [S5], [S12] |
| HTTPS, port 10443, `/ajax/` | The setup web interface: `GET /ajax/<section>/get_config?type=<n>`, and a write as `GET …/set_config?type=<n>&data=<xml>` | [S4], [S7], [S8], [S12] |
| TCP 1256 | The Audyssey calibration protocol: binary frames with JSON payloads | [S9] |
| TCP 1255 | HEOS CLI, for the network player. Out of scope. | [S7] |

The receiver's AIOS UPnP description, which the driver already reads
for the model, names `X_WebAPIPort` and `X_AudysseyPort` [S7]. So the
driver learns the ports from the model.

## The families

The common family goes in plan 14's `spec.settings`. The rest are
Denon brand fields in `spec.denon.settings` and `status.denon`.

| Field | Read | Set | Tier on the AVR-X1700H |
|---|---|---|---|
| `settings.inputs[].label` | `SSFUN ?` → `SSFUN<input> <label>`, `SSFUN END` [S5] | `AppCommand0300.xml` `SetSourceRename` [S5] | Reported. Confirmed after D7. |
| `settings.inputs[].hidden` | `SSSOD ?` → `SSSOD<input> USE\|DEL`, `SSSOD END` [S5] | 10443 `inputs` type 4 [S7] | Reported. needs D6. |
| `denon.quickSelect[].name` | `SSQSNZMA ?` → `SSQSNZMAQS<n> <name>`, `SSQSNZMA END` [S4] | 10443 `general` type 7 [S7] | Reported. needs D6. |
| `denon.zone2.{hpf, channels, hdmiAudio}` | `Z2HPF?`, `Z2CS?`, `Z2HDA?` | `Z2HPFON`, `Z2CSMONO`, `Z2HDA PCM`, documented [S1] | needs D1. The X2700H has none of the three [S1]. |
| `denon.zone2.{bass, treble, levels, autoStandby}` | `Z2PSBAS ?`, `Z2PSTRE ?`, `Z2CV?`, `Z2STBY?` | documented [S1] | Reported, see below |
| `denon.video.{monitor, aspect, processing, resolution, resolutionHdmi}` | `VSMONI ?`, `VSASP ?`, `VSVPM ?`, `VSSC ?`, `VSSCH ?` | documented [S1] | Unsupported: no answer on port 23 (`denon/AGENTS.md:131-140`) |
| `denon.picture.{mode, contrast, brightness, saturation, noiseReduction, enhancer}` | `PV?`, `PVCN ?`, `PVBR ?`, `PVST ?`, `PVDNR ?`, `PVENH ?` | documented; 000 to 100, 050 is 0 [S1] | needs D1 |
| `denon.volume.{limit, mutingLevel, scale}` | `SSVCTZMA ?` → `SSVCTZMALIM OFF\|060\|070\|080`, `SSVCTZMAMLV MUT\|040`, `SSVCTZMADIS REL\|ABS` [S4] | same keys, verified live on an AVR-X3600H [S4] | needs D1 and D3 |
| `denon.locks.{remote, panel}` | none | `SYREMOTE LOCK ON\|OFF`, `SYPANEL LOCK ON`, `SYPANEL+V LOCK ON`, `SYPANEL LOCK OFF`, documented [S1] | Sent once |
| `denon.locks.setup` | `SSLOC ?` [S7]; 10443 `globals` type 8 | none known | Reported |
| `denon.triggers.{1, 2}` | `TR?` → `TR1 ON` | `TR1 ON\|OFF`, documented [S1] | needs D1. The X2700H has none [S1]. |
| `denon.inputAssign` | `SSHDM ?`, `SSDIN ?`, `SSANA ?` [S5, S10]; 10443 `inputs` type 2 | none known | Reported |
| `denon.ampAssign` | `SSPAA ?` [S10]; 10443 `speakers` type 2; 1256 `GET_AVRSTS` [S9] | none | Reported |

An input's `name` is the code `SI` takes, such as `MPLAY`, and its
`label` is the name the receiver shows. A hidden input has no machine,
which is why it lives here and not in `spec.inputs`.

The second zone's tone, levels, and auto standby are common settings.
Plan 14 says per-zone setup takes the common types under
`spec.zones.<zone>.settings`, and no plan builds that yet. Until then
the driver reports them in `status.denon.zone2` and does not accept
them in the spec. The three Denon-only fields are declarable in
`spec.denon.settings.zone2`.

## The second paths

The driver takes port 23 first, for every field it answers there. A
field port 23 cannot reach uses one second path, chosen in this order:
port 10443, then `AppCommand`, then port 1256 for a read.

### Port 10443

The setup web interface sends no authentication challenge. It serves a
self-signed certificate [S7] `avcon/setupapi.py:73-79`, so the driver
does not verify it, the same way the WiiM driver reaches its device
(`wiim/client.go:139`).

**Its capability document.** Every element of a `get_config` answer
carries `display`: `1` is hidden, `2` is shown but not editable, and `3`
is editable. Type 1 of each section is a map of which menus exist
[S8], [S4] `video_config.py`. The driver reads that map and the
`display` attribute before it probes:

| `display` | Kind | Effect |
|---|---|---|
| absent element, or `1` | Refused | Unsupported until the next connect |
| `2` | Not now | Blocked, with the prerequisite in the reason, such as Amp Assign |
| `3` | Settable | the row's ceiling applies |

**Its fragility.** On an AVC-A110, `speakers` types 15 to 19, `network`
type 3, and `general` types 19 to 21 send no answer and hold the web
server for tens of seconds, and other types time out after them. The
server needs about 0.4 seconds between requests [S7] `docs/api-notes.md`.
So the driver sends one request at a time, 0.4 seconds apart, with a
10-second timeout, and only to an allowlist of types:

| Section | Types |
|---|---|
| `globals` | 8 SetupLock, 10 SpeakerPreset |
| `speakers` | 2 AmpAssign, 3 SpeakerConfig, 4 Distances, 5 Levels, 6 Crossovers, 7 Bass |
| `inputs` | 1 the menu map, 2 InputAssign, 3 SourceRename, 4 HideSource |
| `general` | 4 Zone2Setup, 7 QuickSelectNames, 8 and 9 TriggerOut |
| `video` | 1 the menu map, 2 PictureAdjust, 3 HdmiSetup, 4 OutputSettings |

A write is a `GET` to `set_config` with the field's element as `data`.
A `POST` is refused. Most sections take the bare element; the Amp
Assign section takes it wrapped in its root element, and answers HTTP
500 without it [S4] `video_config.py:248-262`. After a write the driver
reads the type again, which is the read-back.

### `AppCommand`

`AppCommand.xml` takes `<cmd id="1">` and serves `GetRenameSource`,
`GetDeletedSource`, and `GetQuickSelectName`. `AppCommand0300.xml`
takes `<cmd id="3">` and serves `GetSourceRename` and
`SetSourceRename` [S5] `XMLCommands.txt`, [S6] `appcommand.py:193-194`.
The commands come from captures of Denon's phone app on an AVR-X1500H,
not from a document. The driver uses `SetSourceRename` only, because
`SSFUN ?` on port 23 reads the same names back.

### Port 1256

`GET_AVRINF` and `GET_AVRSTS` answer JSON that holds `AmpAssign`,
`AssignBin`, `ChSetup`, and `SWSetup` [S9] `oca_transfer.py:1155-1255`.
The driver sends only these two reads. It never sends `ENTER_AUDY` or
any `SET_` command: those enter the calibration mode and write the
Audyssey filters. The driver uses port 1256 only for `ampAssign`, and
only when neither port 23 nor port 10443 answers it.

## Reading the families again

The second paths have no event stream, and the `SS` families on port
23 may not be sent when a person changes them. A timer that reads them
again would be a defect. So the driver reads a family again on these
events, with a one-second debounce for a burst:

* the connect, which reads every family once;
* an apply to the family, which reads it back;
* an `SPPR` line that reports another speaker preset;
* an `MNMEN OFF` line, if drill D2 shows the receiver sends it when a
  person closes the setup menu;
* an `SS` line that no family parses, which the `denonavr` library
  also treats as a sign that the inputs changed [S6] `input.py:214`.

A query with no answer by the end of the first read makes the field
Unsupported, with the reason "no answer to `<query>` during the first
read". The events above ask again. A change made in the receiver's web
interface or menu that sends no port-23 line is not seen until one of
these events. Drill D2 measures how often that happens.

## What plan 13 got wrong

Plans keep their evidence, so the corrections stay here:

* Plan 13 said "undocumented" means the commands the `denonavr` library
  sends. `denonavr` sends none of `SSSPC`, `SSSDE`, `SSCFR`, `SSLEV`,
  `SSSWM`, `SSFUN`, `SSSOD`, or `SSQSNZMA`. Its only `SS` commands are
  `SSTTR` and, on a Marantz, `SSHOSALS` [S6] `api.py:580-660`. The
  sources are [S4], [S5], [S7], and [S10].
* Plan 13 said the operator cites the CY2022 document. The code cites
  Ver.8.6.0 and the AVR-X4000 document (`denon/protocol.go:7`,
  `denon/AGENTS.md:12-17`).
* Plan 13 said the AVR-X1700H reads and sets the `SSHOS` family, and
  called it measured. The receiver took one set command,
  `SSHOSCONSTS LAS`, at the value it already held
  (`denon/AGENTS.md:91-102`). No set to a new value was measured.
* Plan 13 said the video controls answer as remote-key codes on
  `formiPhoneAppDirect.xml`. That endpoint passes a port-23 line through
  HTTP [S6] `const.py:625-635`, [S12]. The `RCKSK` codes are remote
  keys, and Denon documents them for the control protocol itself, in
  the FY21 sheet's Extension COMMAND sheet [S1].
* Plan 13 planned an HTTP HDMI Control press confirmed by `SSHOSCON`.
  The driver already sets `SSHOSCON ON` and `OFF` on port 23
  (`denon/settings_hdmi.go:155-157`).
* Plan 13 put `GetDeletedSource` and `GetQuickSelectName` on
  `AppCommand0300.xml`. They are on `AppCommand.xml` [S5].
* Plan 13 said Amp Assign has no control-port read. `SSPAA ?` is
  untested [S10], and port 1256 reads it [S9].
* Plan 13 said a model answers `unknown command`. A Denon sends no error
  line; it ignores a command it lacks (`denon/protocol.go:25-28`).
* Plan 13 said the AVR-X1700H refuses port 10443, so its 10443 families
  are unsupported. `denon/AGENTS.md:105-117` quotes the same receiver's
  web setup page, its `VideoSettings.js`. The manual reaches web control
  at `http://<address>` and lists a setup menu there [S3 p.159]. On an
  AVC-A110, port 80 redirects to HTTPS [S7]. The two records disagree,
  and drill D6 decides.

## Considered and set aside

* **Port 10443 for everything.** It reaches more families than port
  23, but it has no event stream, and some types hold the web server for
  tens of seconds. Port 23 answers at once and sends events.
* **Polling the second paths.** It reads state again to find a change,
  which the repository's rule forbids, and it loads a fragile server.
* **Port 1256 writes.** `SET_SETDAT` writes the layout inside the
  calibration mode [S9]. A failed session could leave the receiver in
  that mode.
* **`formiPhoneAppDirect.xml` as a second path.** It carries the same
  port-23 lines, so it reaches nothing port 23 does not.
* **A table of models.** The `display` attribute and the answers on port
  23 say what the model has.

## Phases

1. The port-23 reads into `status`: inputs, quick select names, zone 2,
   video and picture, volume, triggers, input assign, and setup lock,
   after drill D1. The reread events.
2. Port 10443: the client, the allowlist, the menu map and `display`,
   and the reads of Amp Assign, the hidden inputs, and the quick select
   names. Port 1256 for Amp Assign, after drills D6 and D9.
3. After plan 14: the declarable fields at the tier each drill showed:
   input labels through `SetSourceRename`, hidden inputs and quick
   select names through port 10443, the zone 2 and picture fields, the
   volume fields, the locks, and the triggers.

## Drills

None has run. Each runs on a home cluster with the AVR-X1700H, through
the query endpoints of [plan 15](15-the-speaker-layout.md), or from the
laptop for the HTTP drills.

* **D1, the reads.** With the main zone on, and again in standby:
  `SSFUN ?`, `SSSOD ?`, `SSQSNZMA ?`, `SSVCTZMA ?`, `SSHDM ?`,
  `SSDIN ?`, `SSANA ?`, `SSLOC ?`, `SSPAA ?`, `PV?`, `PVCN ?`, `TR?`,
  `MNMEN?`, `Z2CV?`, `Z2HPF?`, `Z2PSBAS ?`, `Z2PSTRE ?`, `Z2CS?`,
  `Z2STBY?`, `Z2HDA?`, `Z2QUICK ?`, `VSMONI ?`, `VSASP ?`, `VSVPM ?`,
  `VSSC ?`, and `VSSCH ?`.
* **D3, the volume setters.** Send `SSVCTZMALIM 070`, query
  `SSVCTZMA ?`, and send the old value back.
* **D6, the web setup.** `curl -k` on
  `https://<receiver>:10443/ajax/speakers/get_config?type=2`, then the
  same path on ports 80 and 8080. Record which port served
  `VideoSettings.js`, whether port 80 redirects, and the `display`
  values of each allowlisted type. Repeat with Network Control set to
  Always On [S3 p.159].
* **D7, `AppCommand`.** `POST` `GetSourceRename`, `GetDeletedSource`,
  and `GetQuickSelectName`. Send `SetSourceRename` for one input,
  confirm it with `SSFUN ?`, and send the old label back.
* **D8, a remote key on port 23.** Send `RCKSK0410826` on port 23 and
  record whether `SSHOS ?` reports HDMI Control on.
* **D9, port 1256.** Send `GET_AVRSTS` and nothing else while the
  operator holds port 23. Record the answer, and check that the port-23
  connection stays up.

## How it will be proved

On a home cluster with an AVR-X1700H:

* `kubectl get receiver -o yaml` shows each input's label and whether it
  is hidden, the quick select names, and the Amp Assign, and they match
  the receiver's menu.
* A label changed in `spec.settings.inputs` appears on the receiver's
  display and in `SSFUN ?`.
* A field the model lacks, such as `denon.video.monitor`, is listed in
  `settingExceptions` with the query that had no answer.
* A rename made in the receiver's menu reaches status after the menu
  closes, with no timer in the operator.

## Sources

* [S1] Denon FY21 AVR control protocol V02:
  https://github.com/mkulesh/onpc/blob/master/doc/Denon/FY21_AVR_DENON_PROTOCOL_V02_04062020.xlsx
* [S3] AVR-X1700H owner's manual:
  https://cdn.teufelaudio.com/products/DENON/DENON%20AVR%20X1700H%20DAB/bda-denon_avr_x1700h_dab-en
* [S4] LaserGuruGuy/denon_avr, `protocol_profile.json` and `video_config.py`:
  https://github.com/LaserGuruGuy/denon_avr/tree/main/custom_components/denon_avr/avr
* [S5] mkulesh/onpc, port 23 and XML captures from an AVR-X1500H:
  https://github.com/mkulesh/onpc/tree/master/doc/Denon/Samples
* [S6] ol-iver/denonavr: https://github.com/ol-iver/denonavr
* [S7] plotdot/immersive, `docs/api-notes.md` and `avcon/setupapi.py`:
  https://github.com/plotdot/immersive
* [S8] Reverse engineering the Denon amplifier web API, on an AVR-X3700H:
  https://blog.abbey1.org.uk/index.php/technology/reverse-engineering-the-denon-amplifier-web-api
* [S9] srinivas486/audyssey-rew-tuner, `COMMAND_INVENTORY.md` and
  `oca_transfer.py`: https://github.com/srinivas486/audyssey-rew-tuner
* [S10] frawau/aiomadeavr, `aiomadeavr/avr.py:142-151`:
  https://github.com/frawau/aiomadeavr
* [S12] Denon X3800H control with telnet, HTTP, and HTTPS:
  https://www.heimkinoverein.de/forum/thread/27843-denon-x3800h-homeassistant-steuerung-mit-telnet-http-https-befehle/
