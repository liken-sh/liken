# The WiiM protocol reference

This file lists the WiiM models and the commands of the LinkPlay
HTTP API, with how far to trust each source of a command. It records
what a measured WiiM Amp replies to, and it links the outside
documents. `AGENTS.md` in this directory covers what you need for
every change to the driver.

## The models

The amps:

| Model | Power into 4 ohm | Notes |
| --- | --- | --- |
| WiiM Amp | 120 W per channel | HDMI ARC, optical, line-in, USB-A, sub out, Ethernet |
| WiiM Amp Pro | 120 W per channel | ESS ES9038Q2M, Wi-Fi 6E, no display |
| WiiM Amp Ultra | 200 W per channel | dual TPA3255, 3.5-inch screen, HDMI ARC with Dolby Digital |
| WiiM CI MOD A80 | not stated | rack model for the custom-install line |

The streamers and speakers are not amps, but they reply to the same
API: WiiM Mini, WiiM Pro, WiiM Pro Plus, WiiM Ultra, WiiM Sound, and
WiiM Sound Lite. The WiiM Ultra is a streamer and preamp with an HDMI
ARC input and a phono stage, and it has no power amplifier.

The WiiM Vibelink Amp is an analog power amplifier with no network
port. No protocol reaches it, so the operator cannot control it.

The HDMI port on a WiiM Amp is ARC, so it accepts audio from a
television and not from a source. A `liken` machine connects to the
amp over optical, line-in, or USB audio, not over HDMI.

## The sources of the commands

Three outside documents list these commands, and some are more
trustworthy than others:

- WiiM's own HTTP API document, version 1.2, lists the playback,
  input-switch, equalizer on and off, device-control, alarm, preset,
  and audio-output commands. A command in this document is
  official.
- `DanBrezeanu/wiim-extended-http-api` lists the balance, LED,
  button, Bluetooth, SPDIF-delay, and static-IP commands, taken from
  the requests that the WiiM Home app sends.
- `cvdlinden/wiim-httpapi`'s `openapi.json` lists the most commands,
  including the device name, the band-level and LV2 equalizer, the
  volume ceiling, and the subwoofer controls. It is the least
  trustworthy of the three.

Each family below says which document it comes from.

## The reads

| Command | Returns |
| --- | --- |
| `getStatusEx` | device name, firmware, project, build date, `internet`, group, casting flags |
| `getPlayerStatus` | play state, volume, mute, source, track metadata |
| `getMetaInfo` | the current track's title, artist, album, and cover |
| `getStaticIpInfo` | static address, gateway, DNS |
| `getChannelBalance` | left-right balance from -1.0 to 1.0 |
| `EQGetStat` | whether the equalizer is on |
| `getShutdown` | seconds left on the sleep timer |
| `getPresetInfo` | the stored presets and their numbers |
| `getAlarmClock:<0-2>` | one alarm |
| `getbtpairstatus`, `getbthistory` | Bluetooth pairing state and paired devices |

## The transport and level writes

```
setPlayerCmd:play:<url>       play a URL
setPlayerCmd:resume           resume
setPlayerCmd:pause            pause
setPlayerCmd:onepause         toggle play and pause
setPlayerCmd:stop             stop
setPlayerCmd:prev             previous track
setPlayerCmd:next             next track
setPlayerCmd:seek:<seconds>   seek
setPlayerCmd:vol:<0-100>      set volume
setPlayerCmd:vol++            step volume up
setPlayerCmd:vol--            step volume down
setPlayerCmd:mute:1           mute
setPlayerCmd:mute:0           unmute
setPlayerCmd:loopmode:<n>     0 no loop, 1 single, 2 shuffle, -1 sequence
setPlayerCmd:groupVol:<0-100> set the group volume
```

## The input writes

```
setPlayerCmd:switchmode:wifi
setPlayerCmd:switchmode:optical
setPlayerCmd:switchmode:line-in
setPlayerCmd:switchmode:coaxial
setPlayerCmd:switchmode:bluetooth
setPlayerCmd:switchmode:hdmi       HDMI ARC, on models with an HDMI port
setPlayerCmd:switchmode:usb        USB audio
setPlayerCmd:switchmode:udisk      files on a USB drive
setPlayerCmd:switchmode:phono      on the Ultra
```

WiiM's document lists only `wifi`, `line-in`, `optical`, `udisk`,
and `bluetooth`. The others come from the community sources and from
other models. So the driver treats them as unproven until a model
replies to them.

## The other families

- Presets: `MCUKeyShortClick:<1-12>` starts a stored preset, and
  `getPresetInfo` lists them. Official.
- Equalizer: `EQOn`, `EQOff`, `EQGetStat`, `EQGetList`, and
  `EQLoad:<name>` are official. `EQSet:Bass`, `EQSet:Treble`,
  `EQSetBand`, `EQSetChannelMode`, `EQSave`, and the LV2 and v2
  families are in `openapi.json` only, so the driver treats them as
  unproven until a model replies to them.
- Alarms: `setAlarmClock:<n>:<trig>:<op>:<time>[:<day>][:<url>]`,
  `getAlarmClock:<n>`, `alarmStop`, and `timeSync:<YYYYMMDDHHMMSS>`
  are official, with three alarms at most.
- Device control: `reboot` is official. `setShutdown:<seconds>` and
  `getShutdown` are the sleep timer, not standby.
- Balance: `getChannelBalance` and `setChannelBalance:<n>`, from
  the extended source. The value is from -1.0 to 1.0.
- Bluetooth: `startbtdiscovery:<seconds>`, `getbtdiscoveryresult`,
  `clearbtdiscoveryresult`, `getbthistory`,
  `connectbta2dpsynk:<mac>`, and `disconnectbta2dpsynk:<mac>`, from
  the extended source.
- Device lights and buttons: `LED_SWITCH_SET:<0|1>` and
  `Button_Enable_SET:<0|1>` from the extended source, with
  `LED_SWITCH_GET` and `Button_Enable_GET` in `openapi.json`.
- Audio output: `getNewAudioOutputHardwareMode` and
  `setAudioOutputHardwareMode:<n>` are official.
- Device name: `setDeviceName:<name>`, in `openapi.json` only.
- Queues and grouping: the UPnP AVTransport and `PlayQueue` services,
  not `httpapi.asp`.

## The event subscriptions

The MediaRenderer description on port 49152 lists two event URLs:
`/upnp/event/rendercontrol1` for RenderingControl and
`/upnp/event/rendertransport1` for AVTransport. A measured WiiM Amp
replies to both, and the driver uses them:

- A `SUBSCRIBE` request has the headers `CALLBACK`,
  `NT: upnp:event`, and `TIMEOUT: Second:1800`. The device replies
  `200` with a `SID` and a `TIMEOUT` header such as `Second-1801`.
  The driver renews the subscription 5 minutes before it expires.
  When the driver stops, it sends `UNSUBSCRIBE` with the `SID`.
- The device closes the connection after it replies to a
  `SUBSCRIBE`, even though its reply has `Connection: keep-alive`. A
  second subscription on that connection fails with EOF. So the
  subscription client opens a new connection for each request. Go's
  HTTP transport retries a request that failed on a reused
  connection, so the failure does not always show on the first
  attempt.
- Right after the subscribe, the device sends a `NOTIFY` with
  sequence `0` and the full state of the service. Each later `NOTIFY`
  has only the fields that changed. So the driver merges each later
  `NOTIFY` into its state, and replaces the whole state on sequence
  `0`.
- RenderingControl has `Volume` and `Mute` on the `Master` channel,
  each as a `val` attribute in the device's whole steps.
- AVTransport has `TransportState`. Its track metadata is
  double-escaped DIDL-Lite, with no sample rate, bit depth, or bit
  rate. So the driver uses the transport state from the event, and
  gets the track from the poll's `getMetaInfo`.
- The `CALLBACK` host is the local address that can reach the
  device. The driver finds it by dialing the device, so no person
  declares it.
- A subscription does not change the state of the device. So the
  command counter reports a rejected subscription the same way as a
  failed read.

## A measured WiiM Amp

A WiiM Amp on a home cluster runs firmware `Linkplay.5.2.828335`
(build 20260904), project `WiiM_Amp_4layer`, on an AmlogicA113 SoC.
This section records what it reports and which commands it accepts.

Its open ports are 443 (the API), 49152 (UPnP), 59152 (the port that
mDNS advertises), and 8819 (the port that `getStatusEx` calls
`communication_port`). No document describes a protocol for port 8819,
and the driver does not use it. Ports 80, 1255, 3483, and 8080 are
closed. The TLS certificate is the LinkPlay self-signed pair, CN
`www.linkplay.com`, valid to 2028.

The reads that it replies to:

```
getStatusEx              device identity and capabilities
getPlayerStatus          play state, volume, mute, source
getPlayerStatusEx        the same, plus play mode
getMetaInfo              title, artist, album, cover, sample rate
```

The inputs that it reports are `wifi`, `bluetooth`, `line-in`,
`optical`, and `HDMI`, and `udisk` in the capability list. It has no
phono and no coaxial input. `getSoundCardModeSupportList` reports one
output, `Speaker Out`.

`getPlayerStatus` returns `Title`, `Artist`, and `Album` as
hex-encoded bytes. `getMetaInfo` returns the same fields as plain
JSON, so the driver reads the metadata from `getMetaInfo`.

The writes that it accepts, each with the reply `OK`:

```
setPlayerCmd:vol:<0-100>
setPlayerCmd:mute:<0|1>
setShutdown:<seconds>
```

`setShutdown` is the sleep timer, not standby. A positive number is
the seconds until playback pauses, `0` pauses it immediately, and `-1`
cancels the timer. `getShutdown` reads the remaining seconds. A test
with a silent file served from the LAN watched the count reach zero
and the play state change from `play` to `pause`, while the device
stayed reachable and `power_mode` stayed `-1`.

No command of the API puts an Amp into standby. WiiM's power guide
says that a device goes to standby through the automatic idle timer
or the voice remote's power button, and the app has no power button.
pywiim implements no power command.

On this firmware, it does not reply to `getPowerMode`,
`getAudioOutputStatus`, `getDeviceInfo`, `getFirmwareVersion`,
`getMAC`, `getMultiroomStatus`, `getSlaveList`, or
`getTriggeroutStatus`. Two of the probes used names that no outside
document lists, so their failure proves nothing. The real equalizer
read is `EQGetStat`, and the real output read is
`getNewAudioOutputHardwareMode`. Neither was tried, so the equalizer
and the output mode are untested on this model, not absent.
`getAudioOutputMode` is not a WiiM command.

`getStatusEx` also has a `security` block: `https/2.0`, security
version `3.0`, and an AES capability. The plain HTTPS API still
replies without a token. So on this firmware the block only reports a
capability, and it does not restrict the API.

## The UPnP descriptions

| Device | `manufacturer` | `modelName` | `modelNumber` |
| --- | --- | --- | --- |
| WiiM Amp | Linkplay Technology Inc. | WiiM Amp | V01-Sep 22 2026 |
| Arylic amp | Rakoit Technology(SZ) Co., Ltd. | A50 | V01-Apr 27 2022 |

Both measured devices gave port 59152 in their mDNS `SRV` record,
and port 49152 replied too. The Arylic amp served its description to
the operator's node, but it rejected requests to the HTTPS API from
that node.

## The references

WiiM's own documents:

- [HTTP API for WiiM Products, version 1.2](https://www.wiimhome.com/pdf/HTTP%20API%20for%20WiiM%20Products.pdf), which lists the playback, input-switch, equalizer on and off, device-control, alarm, preset, and audio-output commands
- [HTTP API for WiiM Mini](https://www.wiimhome.com/pdf/HTTP%20API%20for%20WiiM%20Mini.pdf)
- [How to Manage the Power State of Your WiiM Device](https://faq.wiimhome.com/en/support/solutions/articles/72000624590-how-to-manage-the-power-state-of-your-wiim-device), which says that a device goes to standby through the automatic idle timer or the voice remote
- [WiiM Home App Permissions](https://faq.wiimhome.com/support/solutions/articles/72000581676-wiim-home-app-permissions), which says that the setup needs the local network, Bluetooth, and location permissions

Community references that document the protocol beyond WiiM's PDFs:

- [DanBrezeanu/wiim-extended-http-api](https://github.com/DanBrezeanu/wiim-extended-http-api) for the balance, LED, button, Bluetooth, SPDIF-delay, and static-IP commands, taken from the app's requests
- [cvdlinden/wiim-httpapi](https://github.com/cvdlinden/wiim-httpapi), its [`openapi.json`](https://raw.githubusercontent.com/cvdlinden/wiim-httpapi/main/openapi.json), and its published [API reference](https://cvdlinden.github.io/wiim-httpapi/), for the most commands, including the device name, the band-level and LV2 equalizer, the volume ceiling, and the subwoofer controls
- [mjcumming/pywiim](https://github.com/mjcumming/pywiim) for an async client, UPnP eventing, grouping, and [discovery details](https://github.com/mjcumming/pywiim/blob/main/docs/user/DISCOVERY.md)
- [mjcumming/wiim](https://github.com/mjcumming/wiim) for the Home Assistant integration built on pywiim
- [shumatech/wiimplay](https://github.com/shumatech/wiimplay), a Go client
- [carloseberhardt/wiim_api](https://github.com/carloseberhardt/wiim_api), a Rust client
- [tristanreid/wiim-control](https://github.com/tristanreid/wiim-control), a local control panel that proxies the self-signed certificate

Other brands run the same LinkPlay firmware, so their documents
often list a command that WiiM's PDFs leave out:

- [Arylic HTTP API](https://developer.arylic.com/httpapi/), the same firmware with a different brand
- [n4archive/LinkPlayAPI](https://github.com/n4archive/LinkPlayAPI/blob/master/api.md) and [AndersFluur/LinkPlayApi](https://github.com/AndersFluur/LinkPlayApi/blob/master/api.md)

Forum threads that some facts in this file and in `AGENTS.md` come
from:

- [No way of using the Ultra without Internet?](https://forum.wiimhome.com/threads/no-way-of-using-the-ultra-without-internet.6164/) for control on a LAN with no internet route
- [WIIM amp without internet](https://forum.wiimhome.com/threads/wiim-amp-without-internet.5253/) for the same
- [allow API access on http port tcp/80](https://forum.wiimhome.com/threads/allow-api-access-on-http-port-tcp-80.6550/) for the closed port 80 and the self-signed certificate
- [API - http GET works with browser but no other device](https://forum.wiimhome.com/threads/api-http-get-works-with-browser-but-no-other-device.3317/) for the certificate that a client must not verify
- [WiiM HTTP API List](https://forum.wiimhome.com/threads/wiim-http-api-list.9985/) and [Wiim Amp https API - additional commands](https://forum.wiimhome.com/threads/wiim-amp-https-api-additional-commands.3397/) for the command list and the differences between models
- [Is there websocket api documentation for WiiM Pro?](https://forum.wiimhome.com/threads/is-there-websocket-api-documentation-for-wiim-pro.8405/) for UPnP GENA as the event path
- [API Questions](https://forum.wiimhome.com/threads/api-questions.1809/) and [Recall Presets using the Wiim API](https://forum.wiimhome.com/threads/recall-presets-using-the-wiim-api.2968/) for the `PlayQueue` service and `MCUKeyShortClick`
- [Power Off command in App](https://forum.wiimhome.com/threads/power-off-command-in-app.1737/) for the missing power button in the app
- [WiiM Pro API standby mode](https://forum.wiimhome.com/threads/wiim-pro-api-standby-mode.573/) for the same gap in the API
- [API for Wiim Amp?](https://forum.wiimhome.com/threads/api-for-wiim-amp.3306/) for the commands that the Amp replies to
