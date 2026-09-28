---
name: hardware-modules
description: "Make a machine's hardware appear as devices by naming its drivers in spec.modules in load order, with the parameters they need, by declaring in spec.serio the serial-line attachment a USB-CEC adapter needs, and by rebooting when a driver bound the wrong device. Use when a GPU, sound card, radio, sensor, USB controller, or USB-CEC adapter is missing from what a machine publishes, or when status.hardware.unclaimed lists a device."
---

This skill is the guide at https://liken.sh/docs/guides/hardware-modules/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Load the drivers for a machine's hardware

A `liken` machine loads only the drivers its manifest names. Nothing
loads a driver on demand. This guide identifies the hardware a machine
cannot drive, names the drivers for it in load order, sets the
parameters they need, and confirms the device appears. At the end,
the hardware operator that owns the device publishes it.

You need:

* A running machine, with `kubectl` access to its `Machine` resource.
* The machine's manifest in your deployment directory, so the next
  install stick agrees with the cluster.

## Why nothing loads on its own

A Linux distribution loads most drivers on demand: the kernel detects a
device, asks userspace for the driver that matches it, and `modprobe`
or `udev` loads it. `liken` ships neither. The manifest is the whole
truth about what a machine runs, and the boot loads
[`spec.modules`](https://liken.sh/docs/reference/machine/#spec--modules) in the
order the list gives. A driver the list does not name never loads,
and that includes the drivers a subsystem would have pulled in on
its own: a sound codec's parser, a PHY library, a cipher.

The hardware report on the install stick names the drivers for disks
and network ports, in load order. It loads nothing else on purpose,
so every other device needs this guide once.

## 1. Read what the machine cannot drive

The machine reports every device the kernel found and no driver
controls, with the modules whose alias patterns match it:

    kubectl get machine <name> -o jsonpath='{.status.hardware.unclaimed}' | jq

Each entry names the device, its candidate modules in the kernel
build's order of preference, and the correction. More than one
candidate is usual: USB storage matches both `uas` and `usb_storage`,
and the choice is yours. A device that is absent from this list and
absent from every operator's devices has a driver that bound it to
the wrong thing, which the next section covers.

## 2. Work out the driver set

A candidate module is the controller's driver. A controller often
needs sub-drivers loaded before it, or it binds the device to a
generic driver and the device never appears the way the operator
expects. The cases that come up:

* **Sound.** A codec's own driver must load before the controller's.
  With `snd_hda_intel` alone, the controller binds the codec to the
  generic parser, and the outputs never appear. Name the codec
  parser, then the vendor codec driver, then the controller.
* **Network.** A driver can name a soft dependency that must load
  before it, such as a PHY library. Without it the port binds to a
  generic PHY and the link does not come up the same way. A module
  records its soft dependencies in its own `.modinfo` section, and
  `modinfo -F softdep <module>` on a workstation with the same
  kernel family prints them.
* **Bluetooth.** A machine whose adapter serves a remote or a
  keyboard needs `uhid` beside the adapter's driver, and
  [Give a workload a device](https://liken.sh/docs/guides/devices/) says why.
* **USB-CEC adapters.** A Pulse-Eight or RainShadow adapter needs
  three modules: `cdc_acm`, which creates its serial line, `serport`,
  and the adapter's own driver, `pulse8_cec` or `rainshadow_cec`. The
  adapter's driver binds only after the machine attaches the serial
  line, which a `spec.serio` entry declares. See
  [Attach a USB-CEC adapter](#attach-a-usb-cec-adapter).

Write the set down with the sub-drivers first and the controller
last.

## 3. Declare the modules

Add the modules to the machine's manifest, in that order, and apply
it:

    spec:
      modules:
        - snd_hda_codec_hdmi
        - snd_hda_codec_intelhdmi
        - snd_hda_intel

Or patch the live `Machine` and copy the result into the manifest
afterwards:

    kubectl patch machine <name> --type=merge \
      -p '{"spec":{"modules":["snd_hda_codec_hdmi","snd_hda_codec_intelhdmi","snd_hda_intel"]}}'

A merge patch replaces the whole list, so send the full list every
time. An addition loads live, without a reboot. Removing a module
needs a reboot, and a change in the order alone stages for the next
boot with no reboot request, because the machine asks for nothing it
can apply live.

### Attach a USB-CEC adapter

A USB-CEC adapter's driver is a serio driver. It binds to a serio
port, and the kernel creates that port only while a program holds
the `serport` line discipline on the adapter's serial line. On a
general-purpose distribution, `udev` starts
[`inputattach`](https://sourceforge.net/p/linuxconsole/code/ci/master/tree/utils/inputattach.c)
to hold it, as the kernel's
[CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
describes. On `liken`, the machine holds the attachment for the life
of the boot. A
[`spec.serio`](https://liken.sh/docs/reference/machine/#spec--serio) entry names the
protocol and the adapter's USB identity, and the modules go in
`spec.modules` as usual:

    spec:
      modules:
        - cdc_acm
        - serport
        - pulse8_cec
      serio:
        - protocol: pulse8-cec
          usb:
            vendor: "2548"
            product: "1002"

The entry matches by the adapter's vendor and product, not by the
tty name, because the kernel numbers `ttyACM0` and `ttyACM1` in the
order the adapters were plugged in. Add `usb.serial` to match one
unit when a machine has two adapters of one model. An entry with a
serial takes its adapter first, and an entry without one attaches
the adapters of that model that no entry with a serial names. An
added entry attaches without a reboot, and a removed entry stays
attached until the next boot.

An entry withholds the adapter's tty from every workload, because
the machine holds the line. A program that drives the adapter itself
over the tty, such as one built on libCEC, needs no entry and no
`serport` or `pulse8_cec`: declare `cdc_acm` alone, and claim the
tty. `status.hardware.unclaimed` lists an adapter that `cdc_acm`
drives and no entry attaches, and its message names both uses.

A release older than `spec.serio` cannot read a manifest that
declares it. Remove the field before you roll a machine back to such
a release, and let the removal stage for the next boot.
[Roll back](https://liken.sh/docs/guides/rollback/#before-you-roll-back-past-a-spec-field)
gives the steps.

## 4. Set the parameters a driver needs

A load-time setting goes in
[`spec.moduleParameters`](https://liken.sh/docs/reference/machine/#spec--moduleparameters),
keyed `<module>.<parameter>` in the kernel's own spelling:

    kubectl patch machine <name> --type=merge \
      -p '{"spec":{"moduleParameters":{"snd_hda_intel.power_save":"0"}}}'

A parameter applies when its module loads and never after. Declare
the parameter with the module, in one edit, and it applies at the
live load. A parameter added to a module the machine already loaded
applies at the next reboot, and the `ModuleParametersApplied`
condition says so. A parameter cannot reach a module that is built
into the kernel, and the same condition reports that with the fix.

## 5. Reboot when a driver bound the wrong device

Whichever driver is registered when a controller probes keeps the
device until the machine reboots. So a device that bound to the
generic driver before you added its vendor driver stays bound after
the live load. Nothing in the spec asks for a reboot in that case,
because the machine agrees with every document it was given. Ask
for one:

    ./liken request-reboot mycluster <name>

The machine waits for the cluster to grant its turn and drains
before it goes down, under the same policy as any other reboot.
[`liken request-reboot`](https://liken.sh/docs/reference/cli/#liken-request-reboot)
describes the annotation it writes.

## 6. Confirm the device appears

Read the result of every declared module:

    kubectl get machine <name> -o jsonpath='{.status.modules}' | jq

`Loaded` and `Builtin` are the good states. `Missing` means this
kernel has no module by that name, which is usually a misspelling,
because the image includes the kernel's whole module tree. `Failed`
means the kernel refused a module it has, and the message names the
correction. `status.modules[].parameters` shows what the kernel
reports the value for each declared parameter, in the kernel's own rendering.

Read the result of every `spec.serio` entry:

    kubectl get machine <name> -o jsonpath='{.status.serio}' | jq

`Attached` is the good state, and `nodes` lists the devices the
adapter's driver created, such as `/dev/cec0` and the remote's event
node. `Missing` means no serial line matches the entry: the adapter
is unplugged, or `cdc_acm` is not loaded. `Refused` means a line
matches and the attachment did not complete, and the message names
the module to declare, gives the kernel's error, or says that the
adapter's driver did not bind the port. The kernel log then names the
cause. A refused attachment is tried again after a backoff that
grows from one second to five minutes, and at once when the adapter
is plugged in again. The
`SerioAttached` condition names the first entry that is not
attached. It does not change the machine's `Ready` condition,
because a machine with an unplugged adapter still works.

Then read the operator that owns the device. A screen appears in
the display operator's slice, an output in the audio operator's, a
radio in the Bluetooth operator's:

    kubectl get resourceslices

The entry in `status.hardware.unclaimed` is gone once a driver binds
the device.

## 7. Write the list into the manifest

A live patch changes the cluster and nothing else. Copy the final
`spec.modules`, `spec.moduleParameters`, and `spec.serio` into the
machine's manifest in your deployment directory, so the next install
stick and the next reinstall start from the same list. On a fleet run from
git, the commit is the edit, and the machine converges to it.
