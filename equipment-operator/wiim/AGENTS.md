# Working on wiim

This package is the driver for WiiM devices. It controls them with
the LinkPlay HTTP API, and it implements `equipment.Driver`.

Read [`PROTOCOL.md`](PROTOCOL.md) before you add or change a command,
change `events.go` or discovery, or need to know what a model
supports. It lists the commands and how far to trust each source of a
command. It also records what measured devices reply to, and it links
the outside references.

## Control over the LAN

A WiiM replies over the local network to every control that the
operator needs. The driver reads the device's state, and sets the
volume, the mute, the input, and the transport, with unauthenticated
HTTPS `GET` requests to the device's own address. No request goes to
a cloud service. No request needs an account, a token, or a route to
the internet. A measured WiiM Amp replies to reads and accepts writes
this way.

Every request is a `GET`, including a setter. The API is stateless,
so the driver keeps no session open to a device.

The HTTPS listener presents a self-signed certificate (CN
`www.linkplay.com`), so the client does not verify it. On a trusted
LAN the certificate adds no protection. For this reason, WiiM users
have asked the company to open port 80.

A person sets up a WiiM with the WiiM Home app. The app requests the
local network, Bluetooth, and location permissions, and no documented
setup works without the app. After the setup, the device replies on
the LAN whether or not it has a route to the internet. Firmware
updates, the accounts of music services, and voice assistants need
the internet, and the driver uses none of them. The `internet` field
of `getStatusEx` shows whether the device has a route. The driver
only reports that field.

## The transport

The control interface is `httpapi.asp`:

```
https://<address>/httpapi.asp?command=<name>[:<args>]
```

The device returns `OK`, a bare value, or JSON. For a command that
its firmware does not have, it returns `unknown command`. Port 443 is
open on the measured firmware. Port 80 is closed on the newer
LinkPlay modules. Older modules replied on port 80 and served a web
page there. The driver connects to port 443.

Other interfaces of the device are on the same LAN:

- UPnP and SSDP on port 1900. The device description is usually on
  port 49152. The description lists AVTransport, RenderingControl,
  ConnectionManager, and LinkPlay's own `PlayQueue` service in the
  `urn:schemas-wiimu-com:` namespace.
- mDNS, which advertises `_linkplay._tcp.local.`. The Home Assistant
  integration finds devices by this name.
- GENA event subscriptions, which report the changes made at the
  device. No document describes a WebSocket.

## The event path

The driver subscribes to the device's RenderingControl and
AVTransport GENA events, and it also polls the device. The comments
on `pollInterval`, `settingsResync`, `pollFailures`, `trackRetries`,
and `readRetries` in `client.go` give each interval and limit, with
its reason. `sequence.go` explains how a poll keeps a value that an
event or a setter wrote while the poll read. `readback.go` explains
the read after a setter. Read the section "The event subscriptions"
in [`PROTOCOL.md`](PROTOCOL.md) before you change `events.go`.

The poll and the subscriptions follow from what the device does:

- The events report only the volume, the mute, and the transport
  state. No event includes the input, so each poll sends
  `getPlayerStatus`.
- A change made in the WiiM Home app sends no event. The full read of
  every settings family each `settingsResync` is the backstop for it.
- On line-in, optical, and internet radio, the player and
  `getMetaInfo` return different titles. So any reply to
  `getMetaInfo` ends its retries, whatever title it returns.
- When the amp reboots, it drops its subscriptions, but the client
  still has them. So on the first poll that the amp replies to after
  a missed poll, the client renews both subscriptions. If the amp
  rejects a renewal, the client opens a new subscription and reads
  every family.
- An amp that was only cut off from the network keeps its
  subscriptions. So when the client marks the amp unreachable, it
  keeps its subscriptions too. New subscriptions after each outage
  would leave two more on the amp each time. The client clears its
  readings, so the amp's gauges no longer appear in the metrics
  scrape.
- The amp does not send an event again when the client missed it, and
  a renewal does not send the full state. So the poll's read replaces
  each value that no later write changed.
- When the device rejects a subscription, the driver subscribes again
  after `eventRetryInterval` in `events.go`. Until then, the poll is
  the only source of state.

Each status write goes to every watcher of every `Receiver`. So
`status.wiim` leaves out each value that changes by itself between two
reads, such as the clock and the track position. `Readings` in
`readings.go` sends the measurements, such as the temperatures and
the Wi-Fi link, to the `equipment_wiim_*` metrics instead.

No document says what units `wlanDataRate` and
`BleRemoteBatterylevel` use. The driver reads them as Mbit/s and as
percent. These units fit the values that measured amps report: 480
on a 5 GHz link, and 60 or 100 for a remote.

## The identity and the address

Each WiiM has a UUID. `getStatusEx` returns it as `uuid`, 12 bytes in
hex. SSDP's UDN and the mDNS TXT `uuid=` field contain the same 12
bytes followed by `FF98F2F7`. The driver converts between the two
forms. `temp_uuid` is a different value and is not the identity.

The driver identifies a device by its UUID, because the UUID does not
change across a reboot, a change of DHCP lease, or a change of which
network interface is up. One device has four MACs, one each for
Wi-Fi, Bluetooth, the access point, and Ethernet. Both discovery
methods return the UUID next to the address. The device name is a
label that `setDeviceName` changes, and the mDNS instance name comes
from it, so the name is not an identity.

The address is not the identity either. The driver keeps only the
UUID, and finds the address each time it connects:

- mDNS: browse `_linkplay._tcp.local.`, read each instance's TXT
  `uuid` and its address record, and match the UUID.
- SSDP: send an `M-SEARCH` for `MediaRenderer`, read the `USN` of
  each reply and the host in its `LOCATION`, and match the UDN.
- Then read `getStatusEx` at the address it found, and compare the
  `uuid` before it sends any command. This check also applies to a
  declared address. So a changed address or a stale DNS name never
  sends commands to the wrong device.

So in `spec.wiim`, `uuid` is the identity, and the optional `address`
is a hint that the driver tries before discovery. Finding the address
again on a poll or a reconnect is cheap, because the API is
stateless.

mDNS and SSDP use link-local multicast. So discovery works only when
the operator's pod and the amps are on the same L2 segment. A router
or a WireGuard tunnel between them drops multicast. In that case, the
network owner gives each amp a DHCP reservation on its Wi-Fi MAC and
a DNS name, and the spec declares that name.

Discovery creates a `Receiver` only for a WiiM, because other brands
run the same LinkPlay firmware and reply to the same commands. The
section "The UPnP descriptions" in [`PROTOCOL.md`](PROTOCOL.md) gives
what tells a WiiM apart, and `IsWiiM` in `protocol.go` is the check.

## The state

`Status` in `state.go` is the shape of `status.wiim`. The driver
reports one zone, `main`, with no sound mode. The power is always
`On`, because the API has no standby command. A WiiM needs no
Service front, because the protocol has no rule that allows only one
client at a time (`plans/00-design.md`, "The Service front").

The driver reports `Address`, the address that discovery found, and
the controller writes it to `status.address`. This is not
`status.wiim.network.address`, which is the address that the device
reports for its own interface. Because no `spec` field declares the
address, a person reads `status.address` to see where an amp has
moved to.
