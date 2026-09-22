# Working on wiim

This directory holds the WiiM protocol driver, once a plan calls for
it. No code exists here yet. This file collects what a driver needs to
know and where that came from.

## The verdict: the LAN alone operates it

A WiiM answers every control this operator needs over the local
network. It reads state and sets volume, mute, input, and transport
with unauthenticated HTTP GET requests to the device's own address. It
sends no request to a cloud service to do any of that, and it needs no
account, no token, and no internet route. The device does need a local
network, because that is how a client reaches it.

A live WiiM Amp confirms this. Its firmware answers reads and
accepts writes over HTTPS with no authentication, and no request
carries a token.

Two facts come from the design of the protocol, and both help here:

- Every request is a GET, including the ones that set something. The
  API is stateless, so a driver holds no session per device.
- There is no authentication. The HTTPS listener presents a
  self-signed certificate (CN `www.linkplay.com`), so a client disables
  certificate verification. On a trusted LAN, the certificate adds
  nothing, and WiiM users have asked the company to open port 80 for
  exactly this reason.

The one step that is not purely LAN is the first one. A WiiM is
provisioned with the WiiM Home app, which asks for local network,
Bluetooth, and location permission. There is no documented headless
provisioning path. After provisioning, the device runs and answers on
the LAN with or without an internet route.

What still needs the internet: firmware updates, the music-service
accounts behind Tidal and Spotify, and voice assistants. A driver
ignores all three. The `internet` field in `getStatusEx` reports whether
the device has a route, and it is only a report.

## The models

The amps, which are the equipment this operator cares about:

| Model | Power into 4 ohm | Notes |
| --- | --- | --- |
| WiiM Amp | 120 W per channel | HDMI ARC, optical, line-in, USB-A, sub out, Ethernet |
| WiiM Amp Pro | 120 W per channel | ESS ES9038Q2M, Wi-Fi 6E, no display |
| WiiM Amp Ultra | 200 W per channel | dual TPA3255, 3.5-inch screen, HDMI ARC with Dolby Digital |
| WiiM CI MOD A80 | not stated | rack model for the custom-install line |

The streamers and speakers, which are not amps but answer the same API:
WiiM Mini, WiiM Pro, WiiM Pro Plus, WiiM Ultra, WiiM Sound, WiiM Sound
Lite. The WiiM Ultra is a streamer and preamp with an HDMI ARC input
and a phono stage, and it has no power amplifier.

The WiiM Vibelink Amp is a pure analog power amplifier with no network
port. It is not equipment for this operator and no protocol reaches it.

One wiring fact matters for a liken machine. The HDMI port on a WiiM
Amp is ARC, so it takes audio from a television and not from a source.
A liken machine reaches the amp over optical, line-in, or USB audio,
not over HDMI.

## The transport

The control interface is `httpapi.asp`:

```
https://<address>/httpapi.asp?command=<name>[:<args>]
```

Every request uses GET. The device answers `OK`, a bare value, or JSON,
and answers `unknown command` for a command its firmware does not
carry. Port 443 is open on current firmware. Port 80 is closed on the
newer LinkPlay modules, though older modules answered there and served
a web page. A driver connects to 443 and skips certificate
verification.

Three other interfaces are open on the same LAN:

- UPnP/SSDP on port 1900, with the device description usually on port
  49152. The description names AVTransport, RenderingControl, and
  ConnectionManager, plus LinkPlay's own `PlayQueue` service in the
  `urn:schemas-wiimu-com:` namespace.
- mDNS, which advertises `_linkplay._tcp.local.` The Home Assistant
  integration keys its auto-discovery on this name.
- GENA event subscriptions. A client subscribes to AVTransport and
  RenderingControl with a callback URL, and the device sends NOTIFY
  requests when the state changes. This is the push path for track
  changes and volume changes made at the device. There is no
  documented WebSocket. The section below records what a live Amp
  answers on it.

## The event path

The driver subscribes to the device's GENA events, so a change made
at the device reaches the state in under a second. The poll stays, at
ten seconds, for the settings and the identity that push does not
reach, and it carries the evented fields when a subscription is not
live.

The MediaRenderer description is on port 49152. It names the event
URLs `/upnp/event/rendercontrol1` for RenderingControl and
`/upnp/event/rendertransport1` for AVTransport. A live Amp answers
these, and the driver uses them:

- `SUBSCRIBE` carries `CALLBACK`, `NT: upnp:event`, and
  `TIMEOUT: Second:1800`. The device answers `200` with a `SID` and a
  `TIMEOUT` header such as `Second-1801`. The driver renews before the
  grant ends, and sends `UNSUBSCRIBE` with the `SID` when it stops.
- The device closes the connection after it answers a `SUBSCRIBE`,
  while its answer names `Connection: keep-alive`. A second
  subscription on that connection fails with EOF, so the subscription
  client opens a connection for each request. Go's HTTP transport
  retries a request on a reused connection that failed, so the failure
  does not always surface on the first attempt.
- The device pushes a `NOTIFY` with sequence `0` right after the
  subscribe. That body carries the full state of the service. Every
  later `NOTIFY` carries only the fields that moved, so the driver
  merges a delta and replaces on sequence `0`.
- RenderingControl carries `Volume` and `Mute` on the `Master`
  channel, each as a `val` attribute in the device's whole steps.
- AVTransport carries `TransportState`. Its track metadata is
  double-escaped DIDL-Lite, and it has no sample rate, bit depth, or
  bit rate. The driver takes the transport state from the event and
  the track from the poll's getMetaInfo, which carries those fields.
- The `callback` host is the local address the device is reached on,
  found by dialing the device, so the address is discovered and never
  declared.
- The device refuses a subscription it will not carry, and the driver
  then retries once a minute and keeps the poll as the only path. A
  subscription is not a device command that changes state, so a
  refused one is reported through the command counter the same way a
  failed read is.

The poll does not overwrite the evented fields while a subscription is
live. A poll's reads are taken before any event that arrives while it
runs, so writing them would move a value back to where it stood before
the change the device already pushed.

The port 8819 the `getStatusEx` block calls `communication_port` is
open on a live Amp, and no document names a protocol for it. The
driver does not use it.

## The identity and the address

Each WiiM has a UUID that identifies it. `getStatusEx` returns it as
`uuid`, 12 bytes of hex. SSDP's UDN and the mDNS TXT `uuid=` field
spell the same 12 bytes and repeat `FF98F2F7` after them. A driver
normalizes between the two spellings. `temp_uuid` is a different value
and is not the identity.

The UUID is the right key for three reasons:

- It is stable across a reboot and a DHCP lease change, and it does
  not depend on which interface is up.
- It is one value per device. The same device has four MACs, one each
  for Wi-Fi, Bluetooth, the access point, and Ethernet.
- Both discovery paths return it next to the address, so one lookup
  answers both questions.

The device name is not an identity. `DeviceName` and `setDeviceName`
make it a human label, and the mDNS instance name comes from it.

The address is never the identity. A driver resolves the address at
connect time and keeps only the UUID:

- mDNS: browse `_linkplay._tcp.local.`, read each instance's TXT
  `uuid` and address record, and match the UUID.
- SSDP: M-SEARCH for `MediaRenderer`, read each response's `USN` and
  the host in `LOCATION`, and match the UDN.
- Then read `getStatusEx` on the found address and compare `uuid`
  before sending any command. The check also guards a declared
  address, so an address swap never drives the wrong device.

A `PlayQueue:1` service in the UPnP description is the definitive
marker for a LinkPlay device. The `SERVER` header is not, because
LinkPlay and many unrelated devices send the same generic string.

mDNS and SSDP are link-local multicast. Discovery from the operator's
pod works only when the pod shares an L2 segment with the amps. A
router or a WireGuard tunnel between them drops multicast. In that
case the network owner pins each amp with a DHCP reservation on its
Wi-Fi MAC, gives it a DNS name, and the spec declares that name. The
UUID check still runs on connect, so a stale name fails and does not
drive the wrong device.

A `spec.wiim` block therefore declares `uuid` as the identity and an
optional `address` as a hint. A driver reads `uuid` first, tries the
hint, and falls back to discovery. The API is stateless HTTPS, so
re-resolving on each poll and reconnect is cheap.

## The commands

Three outside documents carry these commands, and they do not have the
same authority. WiiM's own HTTP API document, version 1.2, names the
playback, input-switch, equalizer on/off, device-control, alarm,
preset, and audio-output commands; a command that document names is
official. `DanBrezeanu/wiim-extended-http-api` carries the balance,
LED, button, Bluetooth, SPDIF-delay, and static-IP commands, taken by
intercepting the app's own requests. `cvdlinden/wiim-httpapi`'s
`openapi.json` carries the widest surface, including the device name,
the band-level and LV2 equalizer, the volume ceiling, and the
subwoofer controls, and it is the least authoritative of the three.
Each family below says which source carries it.

The named reads:

| Command | Answers |
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

The transport and level writes:

```
setPlayerCmd:play:<url>      play a URL
setPlayerCmd:resume          resume
setPlayerCmd:pause           pause
setPlayerCmd:onepause        toggle play and pause
setPlayerCmd:stop            stop
setPlayerCmd:prev            previous track
setPlayerCmd:next            next track
setPlayerCmd:seek:<seconds>  seek
setPlayerCmd:vol:<0-100>     set volume
setPlayerCmd:vol++           step volume up
setPlayerCmd:vol--           step volume down
setPlayerCmd:mute:1           mute
setPlayerCmd:mute:0           unmute
setPlayerCmd:loopmode:<n>     0 no loop, 1 single, 2 shuffle, -1 sequence
setPlayerCmd:groupVol:<0-100> set the group volume
```

The input writes name the source:

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

WiiM's document names only `wifi`, `line-in`, `optical`, `udisk`, and
`bluetooth`. The rest come from the community sources and other
models, so a driver treats them as unproven until a model answers.

The remaining families:

- Presets: `MCUKeyShortClick:<1-12>` starts a stored preset and
  `getPresetInfo` lists them. Official.
- Equalizer: `EQOn`, `EQOff`, `EQGetStat`, `EQGetList`, and
  `EQLoad:<name>` are official. `EQSet:Bass`, `EQSet:Treble`,
  `EQSetBand`, `EQSetChannelMode`, `EQSave`, and the LV2 and v2
  families are in `openapi.json` only, so a driver treats them as
  unproven until a model answers.
- Alarms: `setAlarmClock:<n>:<trig>:<op>:<time>[:<day>][:<url>]`,
  `getAlarmClock:<n>`, `alarmStop`, and `timeSync:<YYYYMMDDHHMMSS>`
  are official, with three alarms at most.
- Device control: `reboot` is official. `setShutdown:<seconds>` and
  `getShutdown` are the sleep timer, not standby.
- Balance: `getChannelBalance` and `setChannelBalance:<n>` take -1.0
  to 1.0, from the extended source.
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

## The state and the snapshot

`getStatusEx` carries the device's identity: `ssid` is the device
name, `firmware` is the Linkplay version, `project` is the model, and
`internet` is the route flag. `getPlayerStatus` carries the play state,
the volume as an integer from 0 to 100, the mute flag, and the selected
source.

Volume is the one number with a decided meaning. The wire counts whole
steps from 0 to 100, and the display shows the same number, so the
driver reports `VolumeResolution` 1 and needs no conversion.

The driver would map the API onto `equipment.Driver` with one zone
named `main`: power, input, mute, and volume from the fields above, no
sound mode, and the sleep timer where the model has one. The rest of
`getStatusEx` and `getPlayerStatus` becomes the JSON snapshot the
controller writes under `status.wiim`. The design already says a WiiM
makes no Service front, because the protocol has no one-client rule.

## One amp in hand

The studio's WiiM Amp answers on firmware `Linkplay.5.2.828335`
(build 20260904), project `WiiM_Amp_4layer`, on an AmlogicA113 SoC.
These are what it reports, and what it accepts.

Open ports are 443 (the API), 49152 (UPnP), 59152 (the port mDNS
advertises), and 8819 (the port `getStatusEx` calls
`communication_port`). Ports 80, 1255, 3483, and 8080 are closed. The
TLS certificate is the LinkPlay self-signed pair, CN
`www.linkplay.com`, valid to 2028.

The reads it answers:

```
getStatusEx              device identity and capabilities
getPlayerStatus          play state, volume, mute, source
getPlayerStatusEx        the same, plus play mode
getMetaInfo              title, artist, album, cover, sample rate
```

The inputs it reports are `wifi`, `bluetooth`, `line-in`, `optical`,
and `HDMI`, plus `udisk` in the capability list. It has no phono and no
coaxial input. `getSoundCardModeSupportList` reports one output,
`Speaker Out`.

`getPlayerStatus` returns `Title`, `Artist`, and `Album` as hex-encoded
bytes, while `getMetaInfo` returns the same fields as plain JSON. A
driver reads metadata from `getMetaInfo`.

The writes it accepts, each answering `OK`:

```
setPlayerCmd:vol:<0-100>
setPlayerCmd:mute:<0|1>
setShutdown:<seconds>
```

`setShutdown` is the sleep timer, not standby. A positive number is
the seconds until playback pauses, `0` pauses it immediately, and `-1`
cancels the timer. `getShutdown` reads the remaining seconds. A live
test with a silent file served from the LAN watched the count reach
zero and the play state change from `play` to `pause`, while the
device stayed reachable and `power_mode` stayed `-1`.

No command over the API puts an Amp into standby. WiiM's own power
guide says a device stands by through the automatic idle timer or the
voice remote's power button, and the app has no power button. pywiim,
the mature client, implements no power command.

What it does not answer, on this firmware: `getPowerMode`,
`getAudioOutputStatus`, `getDeviceInfo`, `getFirmwareVersion`,
`getMAC`, `getMultiroomStatus`, `getSlaveList`, and
`getTriggeroutStatus`. Two earlier probes used names no outside
document carries, so they prove nothing: the equalizer read is
`EQGetStat` and the output read is `getNewAudioOutputHardwareMode`.
Neither was tried, so the equalizer and the output mode are untested
on this model, not absent. `getAudioOutputMode` is not a WiiM
command.

`getStatusEx` also carries a `security` block: `https/2.0`, security
version `3.0`, and an AES capability. The plain HTTPS API still
answers without a token, so the block reports a capability and does not
gate the API today.

## The references

WiiM's own documents:

- [HTTP API for WiiM Products, version 1.2](https://www.wiimhome.com/pdf/HTTP%20API%20for%20WiiM%20Products.pdf), which carries the playback, input-switch, equalizer on/off, device-control, alarm, preset, and audio-output commands
- [HTTP API for WiiM Mini](https://www.wiimhome.com/pdf/HTTP%20API%20for%20WiiM%20Mini.pdf)
- [How to Manage the Power State of Your WiiM Device](https://faq.wiimhome.com/en/support/solutions/articles/72000624590-how-to-manage-the-power-state-of-your-wiim-device), which states standby is the automatic idle timer or the voice remote
- [WiiM Home App Permissions](https://faq.wiimhome.com/support/solutions/articles/72000581676-wiim-home-app-permissions), which states the setup needs local network, Bluetooth, and location

Community references that document the protocol beyond WiiM's PDFs:

- [DanBrezeanu/wiim-extended-http-api](https://github.com/DanBrezeanu/wiim-extended-http-api) for the balance, LED, button, Bluetooth, SPDIF-delay, and static-IP commands it captured by intercepting the app
- [cvdlinden/wiim-httpapi](https://github.com/cvdlinden/wiim-httpapi), its [`openapi.json`](https://raw.githubusercontent.com/cvdlinden/wiim-httpapi/main/openapi.json), and its published [API reference](https://cvdlinden.github.io/wiim-httpapi/), for the widest command surface, including the device name, the band-level and LV2 equalizer, the volume ceiling, and the subwoofer controls
- [mjcumming/pywiim](https://github.com/mjcumming/pywiim) for an async client, UPnP eventing, grouping, and [discovery details](https://github.com/mjcumming/pywiim/blob/main/docs/user/DISCOVERY.md)
- [mjcumming/wiim](https://github.com/mjcumming/wiim) for the Home Assistant integration built on pywiim
- [shumatech/wiimplay](https://github.com/shumatech/wiimplay), a Go client
- [carloseberhardt/wiim_api](https://github.com/carloseberhardt/wiim_api), a Rust client
- [tristanreid/wiim-control](https://github.com/tristanreid/wiim-control), a local control panel that proxies the self-signed certificate

The LinkPlay stack is shared, so the sibling documents often name a
command WiiM's PDFs omit:

- [Arylic HTTP API](https://developer.arylic.com/httpapi/), the same firmware with a different brand
- [n4archive/LinkPlayAPI](https://github.com/n4archive/LinkPlayAPI/blob/master/api.md) and [AndersFluur/LinkPlayApi](https://github.com/AndersFluur/LinkPlayApi/blob/master/api.md)

Forum threads that carry the facts above:

- [No way of using the Ultra without Internet?](https://forum.wiimhome.com/threads/no-way-of-using-the-ultra-without-internet.6164/) for the LAN-without-WAN question
- [WIIM amp without internet](https://forum.wiimhome.com/threads/wiim-amp-without-internet.5253/) for the same
- [allow API access on http port tcp/80](https://forum.wiimhome.com/threads/allow-api-access-on-http-port-tcp-80.6550/) for the closed port 80 and the self-signed certificate
- [API - http GET works with browser but no other device](https://forum.wiimhome.com/threads/api-http-get-works-with-browser-but-no-other-device.3317/) for the certificate that blocks a plain client
- [WiiM HTTP API List](https://forum.wiimhome.com/threads/wiim-http-api-list.9985/) and [Wiim Amp https API - additional commands](https://forum.wiimhome.com/threads/wiim-amp-https-api-additional-commands.3397/) for the command list and the model differences
- [Is there websocket api documentation for WiiM Pro?](https://forum.wiimhome.com/threads/is-there-websocket-api-documentation-for-wiim-pro.8405/) for UPnP GENA as the event path
- [API Questions](https://forum.wiimhome.com/threads/api-questions.1809/) and [Recall Presets using the Wiim API](https://forum.wiimhome.com/threads/recall-presets-using-the-wiim-api.2968/) for the `PlayQueue` service and `MCUKeyShortClick`
- [Power Off command in App](https://forum.wiimhome.com/threads/power-off-command-in-app.1737/) for the missing app power button
- [WiiM Pro API standby mode](https://forum.wiimhome.com/threads/wiim-pro-api-standby-mode.573/) for the same gap over the API
- [API for Wiim Amp?](https://forum.wiimhome.com/threads/api-for-wiim-amp.3306/) for the Amp's own command coverage
