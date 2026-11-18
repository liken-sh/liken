---
title: Remotes
weight: 30
toc: true
---

<!-- Generated from deploy/remotes-crd.yaml by crdref. Do not edit. -->

# `Remote`

A `Remote` is one physical controller. It selects the controller's
device and, when the model needs one, a
[`Keymap`](/docs/reference/keymaps/). Without a `Keymap`, the base
table maps the arrows, OK, back, and every `KEY_*` code the device
emits. Add a `Keymap` to correct a button that the base table maps
wrongly. A `Remote` does not name a `Player`. Each `Player` lists the
`Remote`s that control it in `spec.remotes`, so one controller can
control several `Player`s.

A `Remote` name has at most 32 characters, because `media-operator`
builds the names of the objects it creates for the `Remote` from it,
and Kubernetes limits some of those names to 63 characters.

    apiVersion: media.liken.sh/v1alpha1
    kind: Remote
    metadata:
      name: den-pad
      namespace: den
    spec:
      device:
        class: gamepad
        selector: device.attributes["bluetooth.liken.sh"].address == "04:4A:5B:11:22:33"
      keymap: dualsense

`spec.device.parameters` is configuration for the driver that
prepares the controller, and `media-operator` copies it unchanged
into the controller's device claim. For example, the Bluetooth
operator's `inputs` parameter chooses which input classes, such as
`joystick`, reach the container that reads the controller. The
Bluetooth operator's manual lists the classes.

    spec:
      device:
        class: gamepad
        selector: device.attributes["bluetooth.liken.sh"].address == "7C:66:EF:22:E7:80"
        parameters:
          driver: bluetooth.liken.sh
          values:
            inputs: [joystick]

One physical controller. `spec.device` selects the controller, and the default key mapping translates its input. When the controller's model needs a different mapping, `spec.keymap` names a Keymap. A Player lists the Remotes that drive it in `spec.remotes`. A Remote does not name a Player.

## spec

The controller to use, and the Keymap for its model when the model needs one.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--device"></span>`device` | [object](#specdevice) | yes | The controller, selected from the devices that the hardware operators publish, and the parameters that its driver uses to prepare it. |
| <span id="spec--keymap"></span>`keymap` | string | no | The name of the Keymap for this controller's model. A Keymap is cluster-scoped, so the name has no namespace. The default mapping passes every `KEY_*` code and turns the hat axes into the arrow keys, so you need a Keymap only for a controller whose codes the kernel names incorrectly. The mapping belongs to the controller and applies on every Player that lists the Remote, as a udev `hwdb` entry applies to a device. |
| <span id="spec--discovery"></span>`discovery` | boolean | no | When true, the Remote's pod logs every input event from the controller, with the code as a Keymap writes it. Use it to find the codes of a controller that has no Keymap: press each button and read the codes in the pod log. In discovery mode the pod reads every input node that the claim provides. Otherwise it reads only the keys and hats that it publishes. The pod publishes keys the same way in both modes, so the controller keeps driving its Player while you map it. Changing this field replaces the Remote's pod, and the controller sends no input for a few seconds. |

### spec.device

The controller, selected from the devices that the hardware operators publish, and the parameters that its driver uses to prepare it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdevice--class"></span>`class` | string | yes | The name of the `DeviceClass` that the claim allocates from. The cluster owner creates this class, so its name is whatever your cluster calls its controllers. |
| <span id="specdevice--selector"></span>`selector` | string | no | A CEL expression over `device.attributes` that matches one controller, for example on its address. When you omit it, the claim can allocate any device of the class. |
| <span id="specdevice--parameters"></span>`parameters` | [object](#specdeviceparameters) | no | Configuration for the driver that prepares the controller. The operator copies it to the claim and does not read it. Use it to set the classes of input that the Remote needs. The manual of the bluetooth operator describes its `inputs` parameter. |

#### spec.device.parameters

Configuration for the driver that prepares the controller. The operator copies it to the claim and does not read it. Use it to set the classes of input that the Remote needs. The manual of the bluetooth operator describes its `inputs` parameter.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeviceparameters--driver"></span>`driver` | string | yes | The name of the driver that reads the parameters, for example `bluetooth.liken.sh`. |
| <span id="specdeviceparameters--values"></span>`values` | object | no | The parameters. The driver defines them, and this operator copies them to the claim. |

## status

What the operator reports about this controller: the Player that receives its key presses now, the paired device that its claim allocated, and the codes that the controller declares and its Keymap does not bind.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--player"></span>`player` | string | no | The Player that receives this Remote's key presses now, whether the Player is idle or playing. Empty while no Player lists this Remote. |
| <span id="status--peripheral"></span>`peripheral` | string | no | The name of the `Peripheral` for the device that this Remote's claim allocated. The name is the device's address in lowercase, with dashes. Read that object for the link state and the battery level. Empty while the claim has no allocation, and for a controller that another driver publishes. |
| <span id="status--unbound"></span>`unbound` | [\[\]object](#statusunbound) | no | The codes that this controller declares and its Keymap does not bind. The list is empty when the Keymap binds every declared code, and absent until the Remote's pod has reported. Each row that you add to the Keymap removes a code from the list, so you can use it to check that a Keymap is complete. |

### status.unbound[]

The codes that this controller declares and its Keymap does not bind. The list is empty when the Keymap binds every declared code, and absent until the Remote's pod has reported. Each row that you add to the Keymap removes a code from the list, so you can use it to check that a Keymap is complete.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusunbound--code"></span>`code` | integer | yes | The raw evdev code, as a number. |
| <span id="statusunbound--name"></span>`name` | string | no | The evdev name for the code. A Keymap binds the code by this name. Empty when the kernel has no name for the code, and a Keymap cannot bind such a code. |
| <span id="statusunbound--type"></span>`type` | string | yes | The evdev event type of the code: `key` for a button, `abs` for a hat axis. One of: `key`, `abs`. |

## The Remote's pod

`media-operator` runs one pod for every `Remote` in the cluster,
whether or not a `Player` lists it. The pod gets the controller through
the `Remote`'s `ResourceClaim`, so it runs on the machine that has the
controller. It reads the controller's evdev nodes, translates each
event through the base key table combined with the `Remote`'s `Keymap`,
and publishes it to the bus under the kernel's key name. For a control
that does not autorepeat, such as a gamepad button, it also generates
the repeat events. Every program that reads the controller's presses
reads them from this one pod.

The pod sets an event mask on each node, so the kernel delivers only
the key events and the hat axes that the pod publishes. A device that
reports motion while it rests then costs the pod nothing. A `Remote`
with `spec.discovery` set has no mask, and its pod reads every event.

The operator copies `spec.device.parameters` into the claim unchanged.
The input driver defines and validates them.

The claim tolerates the `bluetooth.liken.sh/disconnected` taint with no
time limit, so a controller that goes to sleep keeps its allocation
and the pod keeps running. The claim does not tolerate
`bluetooth.liken.sh/no-input-node`, so the pod stays `Pending` until
the controller connects for the first time.

## Status

The operator writes three fields to the `status` of a `Remote`:

* `status.player` is the `Player` that the `Remote`'s focus mark names
  now.
* `status.peripheral` is the name of the `Peripheral` for the device
  that the `Remote`'s claim allocated. Read the controller's link state
  and battery level from that `Peripheral`. It is empty while the claim
  has no allocation, and for a controller that a DRA driver other than
  `bluetooth-operator` publishes.
* `status.unbound` lists the controls that the controller declares and
  that the base table and the `Keymap` together map to nothing, or map
  to `none`. A key code that has a kernel name and no row passes through
  as itself, so it is not unbound. The field is absent until the pod has
  published the controller's codes.

`kubectl get remotes` shows each `Remote`'s `Keymap`, its `Player`, its
`Peripheral`, and its age.

## Events

When a `Remote`'s `Keymap` does not compile, the operator logs the error
and posts a `KeymapRefused` Warning `Event` on the `Remote`, once for
each distinct error. The message contains the compiler's error.
`kubectl describe remote` prints it. The [`keys`](#keys) topic keeps
the last key table that compiled, so the controller keeps working with
that table.

## On the bus

Each `Remote` has its own branch of the [bus](/docs/reference/bus/)
topic tree, `remotes/<namespace>/<name>/`, under the cluster's topic
base. The bus page gives the rules that every topic follows and lists
each topic's writers and readers.

| topic          | writer       | retained | carries                       |
|----------------|--------------|----------|-------------------------------|
| `events`       | the `Remote`'s pod | no       | one key event                 |
| `keys`         | operator     | yes      | the controller's key table    |
| `codes`        | the `Remote`'s pod | yes      | the codes the controller declares |
| `availability` | the `Remote`'s pod | yes      | `online` or `offline`         |
| `focus`        | operator     | yes      | the name of the `Player` the controller drives |
| `focus/cycle`  | the program that has focus | no   | a request to move focus to the next `Player` |

### events

The `Remote`'s pod publishes each event under the kernel's name for the
control, after it translates the event through the base table combined
with the `Keymap`:

    {"key": "KEY_UP", "value": 1}

`value` is the kernel's evdev value: 0 is a release, 1 is a press, and
2 is an autorepeat. A keyboard's own autorepeat passes through. For a
gamepad button or a hat that has a `repeat` block, the pod generates
the value 2 events itself. The pod publishes nothing for a control that
the table maps to nothing. The topic is not retained
([why](/docs/reference/bus/#retained-state-and-events)), so a
subscriber that connects later does not receive an old press.

The operator reads the volume keys from this topic: `KEY_VOLUMEUP` and
`KEY_VOLUMEDOWN` on each press and repeat, and `KEY_MUTE` and
`KEY_UNMUTE` on each press. It changes the volume of the `Player` that
the controller's focus mark names
([how](/docs/reference/players/#volume)). Neither the playback pod nor
the idle screen handles these keys.

### keys

The `keys` topic carries the controller's key table as the operator
compiled it: the base table combined with the `Remote`'s `Keymap`, with
one row for each control. A row gives the evdev type, code, and value,
and the key name. A row for a control that repeats also gives the
repeat delay and interval in milliseconds:

    [{"type": 1, "code": 304, "value": 1, "key": "KEY_ENTER"},
     {"type": 3, "code": 17, "value": -1, "key": "KEY_UP",
      "repeatDelay": 400, "repeatInterval": 250}]

The operator is the only writer. The topic is retained, so the pod
receives the current table as soon as it connects, and an edit to the
`Keymap` reaches the pod without a restart. The operator publishes
again only when the table changes. It clears the topic with an empty
payload when the `Remote` is deleted.

### codes

The pod publishes the key and axis codes that the controller declares.
It reads them from the capability bitmaps of the controller's input
nodes each time it opens the nodes:

    {"keys": [304, 305], "axes": [16, 17]}

The bitmaps list every code that a node can report, so the list is
complete before anyone presses a button. The topic is retained
([why](/docs/reference/bus/#retained-state-and-events)), and the pod
clears it with an empty payload when the nodes disappear. The operator
writes each code here that the key table does not bind to
`status.unbound` on the `Remote`.

### availability

The value is `online` or `offline`, retained. It tells readers whether
the retained `codes` are current (see
[availability](/docs/reference/bus/#availability)). The pod registers
this topic as its MQTT Last Will with the payload `offline`, and
publishes `online` after it connects. When the pod dies, the broker
publishes `offline`, so a reader ignores the codes of a dead pod.

### focus and focus/cycle

The focus mark is the name of the `Player` that this controller drives
now, as plain bytes and not JSON. The operator is the only writer of
`focus`. Every reader of the controller's presses checks the mark
first, and the topic is retained, so presses reach the right `Player`
even while the operator is down. Volume keys are the exception, because
the operator itself handles them. The playback pod's
command sidecar acts only when the mark names the `Player` that its
film runs on. The idle screen of a `Player` that plays nothing acts
only when the mark names that `Player`, and it draws a small hexagon
beside the focused controller in its list of parts.

The operator moves the marks:

* When a `Play` starts on a `Player`, the operator points the mark of
  every controller that the `Player` lists at that `Player`, so the
  controller in a person's hand drives the film they just started.
* When a mark names a `Player` that was deleted, or one that no longer
  lists the controller, the operator moves the mark to the first
  `Player` that lists the controller, in name order.
* When a `Play` finishes, no mark moves. The `Player` keeps focus and
  shows its idle screen.
* When no `Player` lists a controller any more, the operator clears the
  controller's mark with an empty retained payload.

When a person presses `KEY_CYCLEWINDOWS`, the program that has focus
publishes an empty payload on `focus/cycle`: the playback pod's command
sidecar during a film, or the idle screen between films. The topic
name already identifies the controller, so the message needs no
content. The operator moves the mark to the next `Player` that lists
the controller, in name order, wrapping from the last to the first. A
controller that only one `Player` lists stays on that `Player`. The
operator publishes the mark again, and the idle screen pulses its
hexagon.

The idle screen acts on a mark only when a person caused it. A live
mark that moves to its `Player` wakes the screen and pulses the
hexagon, and so does the repeated mark that follows the screen's own
cycle request. Any other repeat of the current mark changes nothing,
so a program that publishes the same mark again does not wake a
sleeping screen.

When the operator restarts, the broker still has each retained mark and
sends it to the operator. A broker that restarted has lost them, so
after each new connection the operator waits two seconds for the
retained marks, then publishes each mark that it has and the broker did
not send.
