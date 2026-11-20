# Working on wiim

This package is the driver for WiiM devices. It controls them with
the LinkPlay HTTP API, and it implements `equipment.Driver`.

Read [`PROTOCOL.md`](PROTOCOL.md) before you add or change a command,
or when you need to know what a model supports. It lists the models,
the commands, and how far to trust each source of a command. It also
records what a measured WiiM Amp replies to, and it links the outside
references.

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

The driver subscribes to the device's GENA events. So a change made
at the device reaches the driver's state as soon as the event
arrives.

The driver also polls the device every 10 seconds. Each poll sends:

- `getStatusEx`, which checks that the device is reachable and is
  the right device
- `getPlayerStatus`, which returns the input, because no event
  includes it

The `getPlayerStatus` reply also has the fields that events report:
the volume, the mute, and the play state. These fields can change
while the poll waits for its reply, and then the poll's value is
older than the change. So the poll applies each of these fields only
when no event or setter wrote it after the poll started its read of
the player. To tell which value is newer, every write of an event or
a setter to one of these fields takes the next number from one
counter. Right before the poll reads the player, it records the
counter. When the poll publishes its result, it keeps each field that
a later write changed (`sequence.go`).

The poll reads more only on these triggers:

- **Another track.** When the player reports another track, the poll
  sends `getMetaInfo`. If the request fails, the next polls send it
  again, at most 3 times in all. Any reply ends the retries, whatever
  title it returns, because on line-in, optical, and internet radio
  the player and `getMetaInfo` return different titles.
- **A sleep timer.** While the last read found a sleep timer running,
  the poll sends `getShutdown`.
- **A full read.** The poll reads every settings family on the first
  poll, on the poll that opens a subscription, and on the first poll
  that the amp replies to after the client marked it unreachable. It
  also reads them 30 minutes after the last full read. That 30-minute
  read is the backstop for a change made in the WiiM Home app, which
  sends no event. The subscriptions open before the full read, after
  `getStatusEx` has checked that it is the right device. When a
  command that replied before fails during a full read, the next poll
  makes another full read, up to 3 full reads in a row. After that,
  the client logs the command once and sends it again only on the
  next full read. The same applies to a command that has never
  replied.
- **A setter.** Right after a setter, the driver reads back that
  setter's family: the balance, `getStatusEx` for the name, the
  controls for the light and the buttons, or `getShutdown` for the
  sleep timer. The read back does not wait for a poll. The
  controller's pass sends the setter, and any poll that is running
  reads the family before it publishes. The read back writes only the
  fields that it read.

When the amp reboots, it drops its subscriptions, but the client
still has them. So on the first poll that the amp replies to after a
missed poll, the client renews both subscriptions. If the amp rejects
a renewal, the client opens a new subscription and reads every
family.

When the client marks the amp unreachable, it clears its readings, so
the amp's gauges no longer appear in the metrics scrape. It keeps its
subscriptions, because an amp that was only cut off from the network
still has them. New subscriptions after each outage would leave two
more on the amp each time. The amp does not send an event again when
the client missed it, and a renewal does not send the full state. So
the poll's read replaces each value that no later write changed.

The driver subscribes to RenderingControl and AVTransport. From their
events it uses only the volume, the mute, and the transport state.
When the device rejects a subscription, the driver tries to subscribe
again once a minute, and in the meantime the poll is the only source
of state. Read the section "The event subscriptions" in
[`PROTOCOL.md`](PROTOCOL.md) before you change `events.go`. It lists
the event URLs and the headers, and describes how the device handles
a connection and a `NOTIFY`.

Each status write goes to every watcher of every `Receiver`. So
`status.wiim` leaves out each value that changes by itself between two
reads:

- the clock (`date`, `time`)
- the track position (`curpos`, `offset_pts`)
- the seconds left on the sleep timer

`Readings` sends these values to the `equipment_wiim_*` metrics
instead:

- the temperatures (`temperature_cpu`, `temperature_tmp102`)
- the Wi-Fi link (`RSSI`, `wlanNoise`, `wlanSnr`, `wlanDataRate`)
- the remote (`BleRemoteBatterylevel`, `BleRemoteRSSI`)

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

Every LinkPlay device serves a UPnP description over plain HTTP at
`/description.xml`, on the port in its mDNS `SRV` record. The
`modelName` in the description tells a WiiM apart from another brand
on the LinkPlay platform. The mDNS TXT records do not include the
model. Discovery creates a `Receiver` only for a device whose
`modelName` starts with `WiiM`, in any mix of capitals. When
discovery cannot read the description, it decides from the `project`
field of `getStatusEx`, such as `WiiM_Amp_4layer`. The driver reads
the same field for `status.model`.

The manufacturer does not identify a WiiM, because LinkPlay makes
devices that other brands sell. A `PlayQueue:1` service in the
description shows that a device is a LinkPlay device. The `SERVER`
header does not, because LinkPlay and many unrelated devices send the
same generic string.

## The state and the snapshot

`getStatusEx` has the device's identity. `ssid` is the device name,
`firmware` is the LinkPlay version, `project` is the model, and
`internet` is the flag for a route to the internet. `getPlayerStatus`
has the play state, the volume as an integer from 0 to 100, the mute
flag, and the selected source. The device's display shows the same
volume number as the wire, so the driver reports `VolumeResolution` 1
and converts nothing.

The driver reports one zone, `main`, with the power, the input, the
mute, the volume, and the sleep timer, and no sound mode. The power
is always `On`, because the API has no standby command. The rest of
`getStatusEx` and `getPlayerStatus` is the JSON snapshot that the
controller writes to `status.wiim`. A WiiM needs no Service front,
because the protocol has no rule that allows only one client at a
time (`plans/00-design.md`, "The Service front").

The driver reports `Address`, the address that discovery found, and
the controller writes it to `status.address`. This is not
`status.wiim.network.address`, which is the address that the device
reports for its own interface. Because no `spec` field declares the
address, a person reads `status.address` to see where an amp has
moved to.
