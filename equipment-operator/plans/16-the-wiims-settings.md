# 16, The WiiM's settings

Proposed on 2026-10-06. Replaces the WiiM part of plan 13
([rejected/13-every-setting-a-receiver-exposes.md](rejected/13-every-setting-a-receiver-exposes.md)).
Plan 14 ([14-one-settings-model-for-every-receiver.md](14-one-settings-model-for-every-receiver.md))
holds the common settings model, the tiers, and `settingExceptions`
that this plan uses. Phase 1 can be built now. Drills D1 to D7 have not
run.

## The problem

A WiiM `Receiver` declares two families today: `device` (`name`,
`led`, `buttons`) and `audio.balance` (`wiim/settings.go:18-34`). Three
faults keep the WiiM from the tier rules that plan 14 states.

**A field the amp cannot read back is sent three times, not once.**
`Pending` treats a nil observed value as a field the device does not
report, and sends it once for each change of the spec
(`wiim/settings.go:105-116`). The controller passes it the value of
`Client.Settings()` (`reconcile.go:491`). That function builds every
field from the stored snapshot with a pointer builder
(`wiim/settings.go:151-163`), so no field is ever nil. A read that the
amp does not answer leaves the snapshot's zero value in place:
`Client.read` returns before the parser runs when the body is
`unknown command`, `Failed`, or empty (`wiim/client.go:464-480`). So on
a model with no `LED_SWITCH_GET`, the snapshot holds `led: false`
(`wiim/client.go:752-757`, `wiim/state.go:294-298`), and a declared
`led: true` differs from it. The operator sends the command, compares
again, and sends it again until the send budget stops it at 3
(`send_budget.go:27`). The field is then named in `SettingsConfirmed`
as unconfirmed, while the amp may hold the declared value. The test
of the sent-once path passes because it builds a nil observed value by
hand (`wiim/settings_pending_test.go:35-49`); the client never does.

**Every setting is read again every 10 seconds.** One poll is 27 GET
requests (`wiim/client.go:29-42`), and it reads the output mode, the
subwoofer, the SPDIF switch delay, the equalizer, the power-mode times,
the presets, the static address, the Bluetooth pairings, the light,
the buttons, and CEC (`wiim/client.go:556-767`). The UPnP events carry
only the volume, the mute, and the transport state
(`wiim/events.go:431-497`). The root `AGENTS.md` section "Keep state
current with events" calls a timer that reads state again to find a
change a defect. The poll also runs before the subscriptions open
(`wiim/client.go:185-196`), which is the reverse of the order that
section requires.

**The common families have no WiiM rows.** Plan 14 moves `name` and
`balance` to `spec.settings`. The WiiM also answers `eq`, `subwoofer`,
`inputs`, and `system.autoStandby` in the common vocabulary, and has
brand fields that no other receiver shares.

## Phase 1: the zero-value fix

A field whose read did not answer is reported as absent. Then a
declared field that the amp cannot read back is sent once, as the CRD
already tells the reader (`deploy/receivers-crd.yaml:274`).

The code path:

1. `Client.read` (`wiim/client.go:464-475`) records, for each read
   command, whether the last answer was a value. A body that `isMiss`
   matches (`wiim/client.go:477-480`) records "no value". A transport
   error records nothing and keeps the earlier answer, because one
   missed request is not the model's answer (the same reason as
   `pollFailures`, `wiim/client.go:45-49`).
2. `Client.Settings()` (`wiim/settings.go:151-163`) returns nil for a
   field whose read command has no value: `getChannelBalance` for
   `Balance` (`wiim/client.go:593`), `LED_SWITCH_GET` for `LED`
   (`wiim/client.go:753`), and `Button_Enable_GET` for `Buttons`
   (`wiim/client.go:758`). `Name` comes from `getStatusEx`, which a
   poll cannot miss (`wiim/client.go:484-496`).
3. `Pending` and `ConfirmedBy` (`wiim/settings.go:63-116`) and
   `setWiimSettings` (`reconcile.go:485-505`) already handle a nil
   field. They do not change.

The test: `startFakeWiim` (`wiim_settings_test.go:37-85`) gains a
variant that answers `unknown command` to `LED_SWITCH_GET`. A declared
`led: true` is sent once, `SettingsConfirmed` does not name it, and a
restart against the same `status.settledSettings` sends nothing.

Phase 1 does not show the reader which fields went to the sent-once
path. Plan 14's `settingExceptions` does that, with the state
`SentOnce`. Phase 1 makes the operator do what the CRD states today.
Plan 14's tier rule is stricter for `led` and `buttons`, whose setters
are undocumented: on a unit that does not answer their reads, phase 3
lists them as Unsupported and sends nothing.

## Phase 2: settings reads leave the 10-second poll

The client reads the settings families at three times, and at no
other time:

* **After the subscriptions are live.** The client opens the
  RenderingControl and AVTransport subscriptions first, then reads
  every settings family once, to set the baseline. An event that
  arrives during the read is not lost.
* **After a write to a family.** The client reads that family once,
  so the confirm and the send budget compare against the amp's answer.
* **On each new subscription.** A subscription that the client opens
  again after a refusal or an expiry means the client may have missed
  events, so it reads every family again. A renewal of a live
  subscription is not a new subscription.

The rest of the poll is out of scope: the input, the track from
`getMetaInfo`, and `getStatusEx` as the reachability probe. Drill D2
can show that AVTransport's `PlaybackStorageMedium` carries the input,
as `pywiim` reads it
([`upnp/eventer.py`](https://github.com/mjcumming/pywiim/blob/main/pywiim/upnp/eventer.py)).
A later plan can then shrink the poll.

### The gap: a change made in the WiiM app

No source names a UPnP variable or other push that carries a setting
of the WiiM. `pywiim` uses UPnP for the volume, the mute, the
transport, and the queue, and the HTTP API for the equalizer, the
output, and the balance
([`docs/design/UPNP_INTEGRATION.md`](https://github.com/mjcumming/pywiim/blob/main/docs/design/UPNP_INTEGRATION.md)).
So when a person changes the equalizer or the balance in the WiiM
Home app, the operator does not see it. Status shows the old value,
and a declared field that the app moved stays moved.

There are two choices:

* **Accept the gap.** The operator reads only at the three times above.
  A change made in the app shows at the next new subscription, which
  can be hours later or never.
* **A long backstop resync.** The client reads every settings family
  again at a long interval, as a backstop for the missing push.

This plan recommends the backstop, at 30 minutes. The root
`AGENTS.md` rule permits a long resync only as a backstop, with a
comment at the timer that gives the failure it covers. Here the
failure is specific and real: the amp sends no event when a setting
changes, so without the backstop the promise in the CRD, that "a
change made at the device is sent back", is false for every WiiM
field. The comment at the timer names that failure and names drill D2
as the evidence. If D2 finds an event that carries a setting, that
family leaves the backstop. A full read of the settings families is
about 15 GET requests. At 30 minutes this is about 30 requests an
hour, against about 9,700 an hour for the whole of today's
poll.

## The common families

These rows fill plan 14's common `spec.settings` for a WiiM. The
tier is a ceiling: on a unit that does not read a field back, a
Confirmed field is sent once and listed as `SentOnce`.

| Field | Setter | Read | Values | Tier | Source |
|---|---|---|---|---|---|
| `system.name` | `setDeviceName:<name>` | `getStatusEx` `DeviceName` | text | Confirmed; D5 owes the read-back proof | [`openapi.json`](https://raw.githubusercontent.com/cvdlinden/wiim-httpapi/main/openapi.json) |
| `system.autoStandby` | `setPowerModeTime:{"idleInterval":"<n>"}` | `getPowerModeTime` `idleInterval` | minutes; the values the model takes are not known | Needs D1 and D3 | `openapi.json` |
| `audio.balance` | `setChannelBalance:<n>` | `getChannelBalance` | -1.0 to 1.0 | Confirmed | [wiim-extended-http-api](https://github.com/DanBrezeanu/wiim-extended-http-api) |
| `eq.enabled` | `EQOn`, `EQOff` | `EQGetBand` `EQStat`, then `EQGetStat` | `On`, `Off` | Confirmed | [WiiM's HTTP API, version 1.2, 2.4](https://www.wiimhome.com/pdf/HTTP%20API%20for%20WiiM%20Products.pdf) |
| `subwoofer.enabled` | `setSubLPF:status:<0\|1>` | `getSubLPF` `status` | on, off | Needs D3 | [`pywiim` `api/subwoofer.py`](https://github.com/mjcumming/pywiim/blob/main/pywiim/api/subwoofer.py) |
| `subwoofer.lowPass` | `setSubLPF:cross:<hz>` | `getSubLPF` `cross` | 30Hz to 250Hz | Needs D3 | same |
| `subwoofer.level` | `setSubLPF:level:<db>` | `getSubLPF` `level` | -15dB to 15dB | Needs D3 | same |
| `inputs[].label` | `setModeRename:<...>` | `getModeRename` | text; `Failed` means no input is renamed | Needs D1 and D3 | `openapi.json`; [`pywiim` `WIIM_DISCOVERED_APIS.md`](https://github.com/mjcumming/pywiim/blob/main/docs/design/WIIM_DISCOVERED_APIS.md) |
| `inputs[].hidden` | none known | `getAudioInputEnable` | per input, 0 or 1 | Reported | same |
| `hdmi.control` | `setCecPowerCtrl:<0\|1>` | `getCecPowerCtrl` | 0 or 1 | Only if D6 shows that it is HDMI control | `openapi.json` |

Notes on the rows:

* **`eq.enabled`.** `EQGetStat` is the read WiiM's document names. It
  answers `unknown command` on some firmware: `pywiim` reports this
  for the WiiM Pro on `Linkplay 4.8` and reads `EQGetBand` instead,
  which returns `EQStat`, `Name`, `channelMode`, `source_name`, and
  the ten bands
  ([`api/eq.py`](https://github.com/mjcumming/pywiim/blob/main/pywiim/api/eq.py)).
  The driver reads `EQGetBand` first and falls back to `EQGetStat`.
  The setters are documented, so a unit that answers neither read
  sends the field once.
* **`subwoofer`.** `getSubLPF` also returns `phase`, `sub_delay`,
  `main_filter`, `sub_filter`, and `mix_sub`. Those are brand fields
  below, because plan 14's common `subwoofer` holds only `enabled`,
  `mode`, `lowPass`, and `level`. The WiiM has no `mode` that
  matches the common one, so a declared `subwoofer.mode` is listed in
  `settingExceptions`. The driver does not parse `status` today
  (`wiim/protocol.go:331-342`). The setters are undocumented, so the
  fields are declarable only after D3 reads each one back. Plan 15's
  second phase builds the declarable `subwoofer` family; this plan
  supplies the WiiM rows.
* **`inputs`.** `getModeRename` answers keys such as `SPDIF-In` and
  `Line-In`, which are not the names `setPlayerCmd:switchmode` takes
  (`optical`, `line-in`). D1 records the keys, and the row maps each
  one to the input name the `Receiver` uses. The setter's argument
  format is not documented; D3 finds it. On this read, `Failed` means
  no input is renamed, which is a value and never makes the field
  Unsupported.
* **`hdmi.control`.** The WiiM's HDMI port is ARC, and the amp
  answers `getCecPowerCtrl`. If D6 shows that turning it off stops the
  amp from following the TV's power and from answering CEC, it is the
  common `hdmi.control`. Otherwise it stays a brand field,
  `spec.wiim.settings.cecPower`.

Tone stays out. `EQSet:Bass:<n>` and `EQSet:Treble:<n>` appear only in
`openapi.json`, with no read and with the argument marked "Unknown".
Plan 14's tier rule keeps an undocumented setter with no read out of
the spec, because nothing can prove it did anything. A declared
`spec.settings.tone` on a WiiM is listed in `settingExceptions`.

## The brand fields

These stay in `spec.wiim.settings` and `status.wiim`, in the WiiM's
own words.

| Field | Setter | Read | Values | Tier |
|---|---|---|---|---|
| `led` | `LED_SWITCH_SET:<0\|1>` | `LED_SWITCH_GET` | on, off | Confirmed |
| `buttons` | `Button_Enable_SET:<0\|1>` | `Button_Enable_GET` | on, off | Confirmed |
| `eq.preset` | `EQLoad:<name>` | `EQGetBand` `Name` | a name from `EQGetList` | Confirmed |
| `output` | `setAudioOutputHardwareMode:<n>` | `getNewAudioOutputHardwareMode`, `getSoundCardModeSupportList` | a sound-card mode string | Confirmed; Reported on a unit with one mode |
| `spdifSwitchDelayMs` | `setSpdifOutSwitchDelayMs:<ms>` | `getSpdifOutSwitchDelayMs` | 0 to 3000 | Needs D3 |
| `maxVolume` | `setMaxVolume:<n>` | `getStatusEx` `max_volume` | 0 to 100 | Reported until plan 14 decides |
| `subwoofer.phase` | `setSubLPF:phase:<0\|180>` | `getSubLPF` `phase` | 0, 180 | Needs D3 |
| `subwoofer.delay` | `setSubLPF:sub_delay:<ms>` | `getSubLPF` `sub_delay` | -200 to 200 ms | Needs D3 |
| `subwoofer.mainFilter`, `subFilter` | `setSubLPF:main_filter:`, `sub_filter:` | `getSubLPF` | 0, 1 | Needs D3 |
| `autoSense` | `setAutoSenseEnable`, `setHDMIAutoSenseEnable` | `getAutoSenseEnable`, `getHDMIAutoSenseEnable` | 0, 1 | Needs D3 |
| `volumeSteps.remote`, `.buttons` | `set_remote_volume_step`, `set_button_volume_step` | `get_remote_volume_step`, `get_button_volume_step` | steps | Needs D3 |
| `fade` | `SetFadeFeature:<0\|1>` | `GetFadeFeature` | 0, 1 | Needs D3 |
| `trigger` | `setTriggeroutStatus:<0\|1>` | `getTriggeroutStatus` | 0, 1 | Needs D7; the Amp does not answer the read |
| `display` | `setLightOperationBrightConfig` | `getLightOperationBrightConfig` | JSON | Needs D7; the Ultra only |
| `inputVolumes` | `setPlayModeVolumeValue` | `getPlayModeVolumeValue` | per input | Reported |
| `presets` | none | `getPresetInfo` | list | Reported |
| `roomCorrection` | out of scope | `RoomCorrGet` | JSON | Reported |
| `network.static` | out of scope | `getStaticIpInfo` | addresses | Reported |
| `bluetooth.paired` | none | `getbthistory` | list | Reported |

Notes on the rows:

* **`led` and `buttons`.** The setters come from the extended source and
  the reads from `openapi.json`. On a unit that does not answer the
  read, plan 14's rule makes the field Unsupported, because an
  undocumented setter with no read cannot be proved.
* **`eq.preset`.** On 5.x firmware the equalizer belongs to each input:
  `EQSetBand` "changes the currently selected source's EQ"
  (`openapi.json`, measured on a WiiM Ultra on firmware 5.2). So
  `EQLoad` sets the preset of the active input only. The operator
  compares the declared preset with the active input's preset, so the
  declared preset reaches each input that becomes active and reports
  another preset. D4 measures this.
* **`output`.** The PDF lists only 1 (`AUDIO_OUTPUT_SPDIF_MODE`), 2
  (`AUDIO_OUTPUT_AUX_MODE`), and 3 (`AUDIO_OUTPUT_COAX_MODE`).
  `pywiim` records 0, 4, 7, and 8 as well, and records that 7 means HDMI
  out on the Amp Ultra and speaker out on the WiiM Sound
  ([`api/constants.py`](https://github.com/mjcumming/pywiim/blob/main/pywiim/api/constants.py)).
  The integer is not one meaning, so the field names the output by the
  `mode` string of `getSoundCardModeSupportList`, such as
  `AUDIO_OUTPUT_SPEAKER_MODE`. The driver reads that list first, as
  plan 14's rule for a capability document requires. A unit whose list
  holds one mode, such as the Amp, reports the field and does not
  accept it.
* **`spdifSwitchDelayMs`.** This is the delay when the optical output
  changes its sample rate. It is not a lip-sync delay
  (`openapi.json`: "SPDIF sample rate switch latency").
* **`maxVolume`.** The amp's own ceiling overlaps `spec.volume.max`.
  [Plan 14's open question](14-one-settings-model-for-every-receiver.md#open-questions)
  asks which one a person declares, so the field is Reported until it
  is answered.
* **`network.static`.** Setters exist (`setWlanStaticIp`,
  `setEthStaticIp`). They stay out by choice: a wrong value cuts the
  operator off from the amp, and only the WiiM Home app can repair it.
* **The answers.** For each read, the driver maps `unknown command` to
  Refused, and a transport error or a timeout to nothing. `Failed` and
  `{"status":"Failed"}` are mapped per row: Refused for `EQGetStat`, a
  value for `getModeRename`. The driver never sends a setter to find
  out whether a model has it, because each setter changes the amp.
  `pywiim` makes the same choice for `LED_SWITCH_SET`.

## Corrections to plan 13's WiiM section

* Only the equalizer and the output mode have documented setters.
  Tone and the subwoofer come from `openapi.json` and `pywiim`.
* The equalizer read is `EQGetBand` first, then `EQGetStat`.
* `hardware: 7` is `AUDIO_OUTPUT_SPEAKER_MODE`, the Amp's one mode,
  which its sound-card list names "Speaker Out". The PDF's list of
  modes is incomplete; the reading does not contradict it.
* The "SPDIF delay" is the sample-rate switch latency, not lip sync.
* `inputs` has WiiM reads and a setter, and `hdmi` may have one.
  `network` has setters, and leaving them out is a choice.
* `{"status":"Failed"}` does not always mean the model lacks a field.
* The status already reports most of these families
  (`wiim/client.go:556-767`). Only the spec held two.
* Plan 13 said `reboot` fails on an Amp. `pywiim` reboots an Amp
  with `StartRebootTime:1`
  ([`api/diagnostics.py`](https://github.com/mjcumming/pywiim/blob/main/pywiim/api/diagnostics.py)).
* The two records of the Amp come from one commit, `929c4133`:
  `wiim/AGENTS.md` says the two reads were not tried, and the test
  fixtures of the same commit give `hardware: "7"` as a live shape.
  D1 settles both.

## Considered and set aside

* **Keep the settings on the 10-second poll.** It is the defect the root
  `AGENTS.md` names, and this plan adds families to it.
* **A table of models.** The output integers differ by model, and
  `pywiim` keeps a catalog of models for this reason. The sound-card
  list answers the same question from the unit itself.
* **Find support by sending a setter.** Every WiiM setter changes the
  amp, so a probe is a change nobody declared.

## Phases

1. The zero-value fix. Buildable now.
2. The settings reads leave the poll, with the 30-minute backstop.
   Buildable now, before plan 14.
3. After plan 14: the WiiM field table in plan 14's engine, with the
   rows for `system.name`, `audio.balance`, `eq.enabled`, `led`,
   `buttons`, `eq.preset`, `output`, and the Reported rows, and the
   answer mapping.
4. After the drills: each row marked "Needs" at the tier its drill
   shows.

## How it will be proved

On a home cluster with a WiiM Amp:

* With `LED_SWITCH_GET` answering `unknown command` in the fake,
  a declared `led` is sent once. On the Amp, a declared `balance` is
  sent, read back, and not sent again after an operator restart.
* The operator's log shows no settings read between the backstop
  intervals, and a change of the balance in the WiiM Home app shows in
  `status.settings.audio.balance` within 30 minutes.
* A declared `eq.enabled: false` turns the equalizer off, and the app
  shows it.

## Drills

None of these drills has run. D1 is read-only. The others change a
setting and put it back.

* **D1, the read sweep on the Amp.** Record the raw answer of
  `EQGetStat`, `EQGetBand`, `EQGetList`, `getNewAudioOutputHardwareMode`,
  `getSoundCardModeSupportList`, `getSubLPF`, `getSpdifOutSwitchDelayMs`,
  `getModeRename`, `getAudioInputEnable`, `getAutoSenseEnable`,
  `getHDMIAutoSenseEnable`, `getCecPowerCtrl`, `getPowerModeTime`,
  `LED_SWITCH_GET`, `Button_Enable_GET`, `GetFadeFeature`,
  `getChannelMode`, `RoomCorrGet`, and `GetAcousticCapability`.
* **D2, the events.** Record the sequence-0 `NOTIFY` bodies of
  RenderingControl, AVTransport, and `PlayQueue`. Then change the
  equalizer, the balance, the light, the subwoofer, an input label,
  and the input in the WiiM Home app, and record each `NOTIFY` that
  arrives.
* **D3, the read-back.** For each row marked "Needs D3", send the
  setter, read the field back, check the app, and put the old value
  back. For `setModeRename`, find the argument format. For
  `setPowerModeTime`, find the values the Amp takes.
* **D4, the equalizer per input.** `EQLoad` a preset on `optical`,
  switch to `line-in`, and read `EQGetBand`.
* **D5, the owed proofs.** `setDeviceName`, `LED_SWITCH_SET`, and
  `Button_Enable_SET` each read back at the value sent.
* **D6, CEC.** With `setCecPowerCtrl:0`, check whether the amp still
  follows the TV's power and still answers on the bus. Put it back.
* **D7, a second model.** On a WiiM Pro or Ultra:
  `setAudioOutputHardwareMode` across the modes its sound-card list
  names, `getTriggeroutStatus`, and `getLightOperationBrightConfig`.
  Then upgrade or reboot it and check that a Refused field is asked
  again on the next connect.
