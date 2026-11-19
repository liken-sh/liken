---
title: Load the drivers for a machine's hardware
weight: 75
description: "Make a machine's hardware appear as devices by naming its drivers in spec.modules in load order, with the parameters they need, by declaring in spec.serio the serial-line attachment a USB-CEC adapter needs, and by rebooting when a driver bound the wrong device. Use when a GPU, sound card, radio, sensor, USB controller, or USB-CEC adapter is missing from what a machine publishes, or when status.hardware.unclaimed lists a device."
---

# Load the drivers for a machine's hardware

A `liken` machine loads only the drivers that its manifest names, and
never loads a driver on demand. In this guide you find the hardware
that a machine has no driver for, list its drivers in load order, set
their parameters, and check that the device appears. The operator for
that kind of hardware then publishes it.

You need:

* A running machine, with `kubectl` access to its `Machine` resource.
* The machine's manifest in your deployment directory, so the next
  install stick agrees with the cluster.

## Why nothing loads on its own

A Linux distribution loads most drivers on demand: the kernel detects a
device, asks userspace for the driver that matches it, and `modprobe`
or `udev` loads it. `liken` has neither. At boot it loads the modules
in [`spec.modules`](/docs/reference/machine/#spec--modules), in the
order that the list gives. A driver that the list does not name never
loads. That includes a driver that another driver would normally pull
in, such as a sound codec's parser, a PHY library, or a cipher.

The hardware report on the install stick lists the drivers for disks
and network ports, in load order. It loads no other drivers, so every
other device needs the steps in this guide.

## 1. Read what the machine cannot drive

The machine reports each device that the kernel found and that no
driver controls, with the modules whose alias patterns match it:

    kubectl get machine <name> -o jsonpath='{.status.hardware.unclaimed}' | jq

Each entry names the device, its candidate modules in the kernel
build's order of preference, and what to change. A device often has
more than one candidate: USB storage matches both `uas` and
`usb_storage`, and you choose one. A device that is in neither this
list nor any operator's devices is bound to the wrong driver. Step 5
covers that case.

## 2. Work out the driver set

A candidate module is the controller's driver. A controller often
needs other drivers to load before it. Without them it binds the
device to a generic driver, and the device does not appear in the form
that the operator expects. These cases are common:

* **Sound.** A codec's own driver must load before the controller's.
  With `snd_hda_intel` alone, the controller binds the codec to the
  generic parser, and the outputs never appear. List the codec
  parser, then the vendor codec driver, then the controller.
* **Network.** A driver can declare a soft dependency that must load
  before it, such as a PHY library. Without it the port binds to a
  generic PHY, and the link can come up differently. A module
  records its soft dependencies in its own `.modinfo` section, and
  `modinfo -F softdep <module>` on a workstation with the same
  kernel family prints them.
* **Bluetooth.** A machine whose adapter serves a remote or a
  keyboard needs `uhid` beside the adapter's driver, and
  [Give a workload a device](/docs/guides/devices/) says why.
* **USB-CEC adapters.** A Pulse-Eight or RainShadow adapter needs
  three modules: `cdc_acm`, which creates its serial line, `serport`,
  and the adapter's own driver, `pulse8_cec` or `rainshadow_cec`. The
  adapter's driver binds only after the machine attaches the serial
  line, which a `spec.serio` entry declares. See
  [Attach a USB-CEC adapter](#attach-a-usb-cec-adapter).

Put the sub-drivers first and the controller last.

## 3. Declare the modules

Add the modules to the machine's manifest in that order, and apply
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
time. A module that you add loads live, without a reboot. Removing a
module needs a reboot. A change in the order alone takes effect at the
next boot, and the machine does not request a reboot for it.

### Run virtual machines

`status.hardware.unclaimed` does not list the modules that run virtual
machines, because the CPU already has a driver. To let a pod boot a
virtual machine, declare the module for the machine's CPU vendor,
`kvm_intel` or `kvm_amd`. Add `vhost_net` for a guest whose tap
network QEMU moves in the kernel, and `vhost_vsock` for a guest with a
vsock address:

    spec:
      modules:
        - kvm_intel
        - vhost_net
        - vhost_vsock

The node then publishes the devices `misc-kvm`, `misc-vhost-net`, and
`misc-vhost-vsock`, and a pod can claim them
([Run a virtual machine in a pod](/docs/guides/devices/#6-run-a-virtual-machine-in-a-pod)).
If `status.modules` reports `kvm_intel` or `kvm_amd` as `Failed`,
virtualization is usually turned off in the firmware setup, under a
name such as VT-x, VMX, SVM, or AMD-V.

### Attach a USB-CEC adapter

A USB-CEC adapter's driver is a serio driver. It binds to a serio
port, and the kernel creates that port only while a program keeps the
`serport` line discipline attached to the adapter's serial line. On a
general-purpose distribution, `udev` starts
[`inputattach`](https://sourceforge.net/p/linuxconsole/code/ci/master/tree/utils/inputattach.c)
to do this, as the kernel's
[CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
describes. On `liken`, the machine keeps the attachment until it
shuts down. A [`spec.serio`](/docs/reference/machine/#spec--serio)
entry gives the protocol and the adapter's USB identity, and the
modules go in `spec.modules` as usual:

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

The entry matches the adapter by its vendor and product, not by its
tty name, because the kernel numbers `ttyACM0` and `ttyACM1` in the
order that the adapters were plugged in. Add `usb.serial` to match one
unit when a machine has two adapters of one model. An entry with a
serial matches its adapter first. An entry without a serial attaches
the adapters of that model that no entry with a serial matches. A
new entry attaches without a reboot. A removed entry stays attached
until the next boot.

While an entry attaches the adapter, no workload can claim its tty,
because the machine uses the serial line itself. A program that drives
the adapter over the tty, such as one built on libCEC, needs no entry
and no `serport` or `pulse8_cec`. Declare `cdc_acm` alone, and claim
the tty. `status.hardware.unclaimed` lists an adapter that `cdc_acm`
drives and that no entry attaches, and its message describes both
ways to use it.

A release older than `spec.serio` cannot read a manifest that
declares it. Before you roll a machine back to such a release, remove
the field and let the removal take effect at the next boot.
[Roll back](/docs/guides/rollback/#before-you-roll-back-past-a-spec-field)
gives the steps.

## 4. Set the parameters a driver needs

A load-time setting goes in
[`spec.moduleParameters`](/docs/reference/machine/#spec--moduleparameters),
keyed `<module>.<parameter>` in the kernel's own spelling:

    kubectl patch machine <name> --type=merge \
      -p '{"spec":{"moduleParameters":{"snd_hda_intel.power_save":"0"}}}'

A parameter applies only when its module loads. Declare the parameter
and the module in one edit, and the parameter applies when the module
loads live. A parameter that you add for a module that the machine
already loaded applies at the next reboot, and the
`ModuleParametersApplied` condition reports this. A parameter cannot
reach a module that is built into the kernel, and the same condition
reports that, with the fix.

## 5. Reboot when a driver bound the wrong device

A device stays bound to the driver that took it when the kernel
probed it, until the machine reboots. So a device that bound to the
generic driver before you added its vendor driver stays on the generic
driver after the vendor driver loads live. The machine does not
request a reboot for this, because every module in its spec is loaded.
Request one yourself:

    ./liken request-reboot mycluster <name>

The machine waits for the cluster to grant its turn, and drains
before it goes down, as for any other reboot.
[`liken request-reboot`](/docs/reference/cli/#liken-request-reboot)
describes the `Disruption` it files.

## 6. Confirm the device appears

Read the result of every declared module:

    kubectl get machine <name> -o jsonpath='{.status.modules}' | jq

`Loaded` and `Builtin` mean the module is available. `Missing` means this
kernel has no module with that name, which is usually a misspelling,
because the image includes the kernel's whole module tree. `Failed`
means the kernel refused a module that it has, and the message says
what to change. `status.modules[].parameters` shows the value that the
kernel reports for each declared parameter, in the kernel's own
format.

Read the result of every `spec.serio` entry:

    kubectl get machine <name> -o jsonpath='{.status.serio}' | jq

`Attached` means the adapter is attached, and `nodes` lists the
devices that the adapter's driver created, such as `/dev/cec0` and the
remote's event node. `Missing` means no serial line matches the entry: the adapter
is unplugged, or `cdc_acm` is not loaded. `Refused` means a line
matches and the attachment failed. The message names the module to
declare, gives the kernel's error, or says that the adapter's driver
did not bind the port. The kernel log has the cause. The machine
retries a refused attachment after a delay that grows from one second
to five minutes, and retries at once when the adapter is plugged in
again. The `SerioAttached` condition names the first entry that is
not attached. It does not change the machine's `Ready` condition,
because a machine with an unplugged adapter still works.

Then check that the operator for that hardware publishes the device.
A screen appears in the display operator's `ResourceSlice`, an audio
output in the audio operator's, and a radio in the Bluetooth
operator's:

    kubectl get resourceslices

When a driver binds the device, its entry leaves
`status.hardware.unclaimed`.

Some USB devices still do not work after their drivers load. They
power up as a bare chip with no program, and the host must upload
firmware into the chip each time it gets power. Many astronomy
cameras built on a Cypress FX2 or FX3 chip work this way. The OS
image does not include that firmware, because it comes from the
vendor.
[`firmware-operator`](https://liken.sh/firmware/docs/guides/upload-to-a-device/)
uploads it from the vendor's container image.

## 7. Write the list into the manifest

A live patch changes only the cluster. Copy the final `spec.modules`,
`spec.moduleParameters`, and `spec.serio` into the machine's manifest
in your deployment directory, so the next install stick and the next
reinstall start from the same list. On a fleet that runs from git, the
commit is the edit, and the machine converges to it.
