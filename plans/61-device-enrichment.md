# Device enrichment

Every slice device should publish the durable facts its source
already reports, in two layers at once: the raw code, so nothing is
lost, and the unpacked flags, so a CEL selector never does bit
arithmetic. liken's machine operator already publishes this way. This
plan brings the three device operators to the same standard, and it
runs ahead of milestone 60, because that milestone's classes and
taints select on facts that only this enrichment publishes.

## The rules

These rules already govern liken's `inventoryDevices` and the display
operator's EDID attributes. The enrichment applies them everywhere.

* **Publish the raw code and the unpacked form together.** The raw
  value answers a question nobody predicted. The unpacked flag is
  what a class selects on.
* **Absent, never empty.** Publish an attribute when the source
  reports the fact, and publish nothing when it does not. `has()`
  answers false for the absent one, and a flag that is false is
  absent. An empty string matches every device that stated nothing.
* **Bare names.** Every attribute here describes what one driver's
  device reports, so it belongs in that driver's own domain. The
  qualified domains, `monitor.liken.sh` and `sound.liken.sh`, stay
  reserved for facts more than one driver stamps.
* **Durable facts only.** A fact that changes on its own, a battery
  percentage or a signal strength, is not an attribute. Every
  attribute write rewrites the slice, and each slice write wakes
  every DRA-pending pod in the cluster.
* **The budget is 32.** The API allows 32 attributes and capacities
  per device. A vocabulary that mirrors everything a source can say
  does not fit, so each operator names the facts it publishes and
  leaves the rest in the raw code.

## What each repository publishes today

| Repository | Attributes per device |
|---|---|
| liken | `bus`, `address`, `driver`, `class`, `classCode`, `subsystem`, `name`, `modalias`, `serial`, `vendor`, `product`, plus `renderNode`, `displayNode`, `sound.liken.sh/supportsSound` |
| Bluetooth operator | `address`, `connected`, `name` |
| display operator | `connector`, `appId`, plus from the EDID: `manufacturer`, `model`, `serial`, `monitor.liken.sh/id`, `widthPixels`, `heightPixels`, `widthMillimeters`, `heightMillimeters` |
| audio operator | `output`, `card`, `pcm`, `connectionType`, `sinkName`, plus from the ELD: `manufacturer`, `product`, `monitorName`, `lpcmChannels`, `speakers`, `monitor.liken.sh/id` |

liken needs no change. It already publishes both layers of the PCI
and USB identity, absent-when-empty, and this plan takes its form as
the model.

## The Bluetooth operator

This is the bulk of the plan. The operator publishes three attributes
today, and BlueZ holds every fact below as a `Device1` D-Bus
property, on the same interface the operator already reads `Alias`
and `Connected` from. Nothing reads the bond `Secret`: that stays
opaque BlueZ storage, copied byte for byte.

The class facts arrive in the inquiry response during the scan. The
UUID list arrives from the SDP browse after pairing, so those
attributes can land a reconcile pass after the device itself. The
write-on-divergence loop already absorbs that shape: the bond store
takes the info file and the cache file on different passes today.

| Attribute | Type | Source |
|---|---|---|
| `classOfDevice` | int | `Class`, the raw 24-bit word |
| `appearance` | int | `Appearance`, the LE equivalent of the class |
| `modalias` | string | `Modalias`, the PnP vendor and product |
| `icon` | string | `Icon`, BlueZ's class-to-name mapping |
| `addressType` | string | `AddressType`, `public` or `random` |
| `majorClass` | string | class bits 12 to 8, as a name |
| `minorClass` | string | class bits 7 to 2, read under the major |
| `serviceAudio`, `serviceRendering`, `serviceCapturing`, `serviceTelephony`, `serviceNetworking`, `serviceObjectTransfer`, `servicePositioning`, `serviceInformation` | bool | class bits 23 to 13, one flag each, absent when clear |
| `audioSink` | bool | UUID `0x110B` |
| `audioSource` | bool | UUID `0x110A` |
| `avrcpTarget` | bool | UUID `0x110C` |
| `avrcpController` | bool | UUID `0x110F` |
| `handsfree` | bool | UUID `0x111E` |
| `headset` | bool | UUID `0x1108` |
| `input` | bool | UUID `0x1124` or `0x1812` |
| `battery` | bool | UUID `0x180F` |
| `serialPort` | bool | UUID `0x1101` |

The profile rows are a named vocabulary, not a mirror of the UUID
list. A UUID this table does not name publishes nothing, and a future
need adds a row. The classic HID and the HID-over-GATT UUIDs collapse
into one `input` flag, because a consumer asks whether the device is
an input device, never which transport carries the reports.

A worked example, the studio's B06+ receiver after this change:

```yaml
- name: e3-28-e9-23-21-6f
  attributes:
    address:          {string: "E3:28:E9:23:21:6F"}
    name:             {string: studio-pa}
    connected:        {bool: false}
    classOfDevice:    {int: 2884632}
    modalias:         {string: "bluetooth:v000ApFFFFdFFFF"}
    icon:             {string: audio-headphones}
    addressType:      {string: public}
    majorClass:       {string: audio-video}
    minorClass:       {string: headphones}
    serviceAudio:     {bool: true}
    serviceRendering: {bool: true}
    serviceCapturing: {bool: true}
    audioSink:        {bool: true}
    audioSource:      {bool: true}
    avrcpTarget:      {bool: true}
    avrcpController:  {bool: true}
    serialPort:       {bool: true}
```

Milestone 60 draws on this directly: the media bus device's kind
attribute joins this vocabulary, the `no-input-node` taint's fact
becomes selectable as `input`, and the audio operator can see which
bonds are speakers in the API instead of browsing the bus itself.

## The display operator

The EDID parser stops at the identity and the preferred mode's size.
The same bytes carry one more durable fact worth a selector:

| Attribute | Type | Source |
|---|---|---|
| `refreshMillihertz` | int | the preferred detailed timing's pixel clock over its total raster, in millihertz so 59.951 Hz survives as 59951 |

Whether the monitor takes audio stays with the audio operator, whose
ELD attributes already state it, joined to this operator's device by
`monitor.liken.sh/id`.

## The audio operator

The ELD block the parser already reads carries the LPCM rates and
depths in the same short audio descriptors that `lpcmChannels` comes
from:

| Attribute | Type | Source |
|---|---|---|
| `lpcmMaxRateHz` | int | the highest rate bit in the LPCM descriptor |
| `lpcmBitDepths` | string | the depth bits, as `"16 20 24"` |

## What was considered and set aside

* **A battery percentage attribute.** BlueZ's `Battery1` reports it,
  but it changes on its own, and every change would rewrite the slice
  and wake every DRA-pending pod. The durable `battery` flag says the
  device reports one; a consumer that wants the number reads it live.
* **RSSI and TxPower.** They exist only during discovery, so they are
  scan facts, and the `PairingRequest`'s `seen` list is where scan
  facts go.
* **The UUID list as one string attribute.** It fits in 64 characters
  only by accident of the device, and a selector would match it by
  substring, which is the bit arithmetic this plan exists to remove.

## What a drill must show

The drill runs on liken-1, against the devices already paired there.

1. The B06+ publishes the worked example above, and the class
   attributes appear even while it is disconnected, because BlueZ
   reports them from the bond it loaded.
2. The DualSense publishes `majorClass: peripheral`,
   `minorClass: gamepad`, `input: true`, and no service flags,
   because its class word sets none.
3. A `resourceclaim` with a CEL selector on `input` allocates the
   DualSense and never the B06+, with no driver or address named.
4. On the display side, liken-1's monitor publishes its refresh rate,
   and the value matches what the compositor reports for the enabled
   mode.
