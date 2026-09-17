---
name: hardware-modules
description: "Make a machine's hardware appear as devices by naming its drivers in spec.modules in load order, with the parameters they need, and by rebooting when a driver bound the wrong device. Use when a GPU, sound card, radio, sensor, or USB controller is missing from what a machine publishes, or when status.hardware.unclaimed lists a device."
---

This skill is the guide at https://liken.sh/docs/guides/hardware-modules/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Load the drivers for a machine's hardware

A `liken` machine loads only the drivers its manifest names. Nothing
loads a driver on demand. This guide finds the hardware a machine
cannot drive, names the drivers for it in load order, sets the
parameters they need, and confirms the device appears. At the end,
the hardware operator that owns the device publishes it.

You need:

* A running machine, with `kubectl` access to its `Machine` resource.
* The machine's manifest in your deployment directory, so the next
  install stick agrees with the cluster.

## Why nothing loads on its own

A Linux distribution loads most drivers on demand: the kernel finds a
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
needs sub-drivers loaded before it, or it binds what it finds to a
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
because the image carries the kernel's whole module tree. `Failed`
means the kernel refused a module it has, and the message names the
correction. `status.modules[].parameters` shows what the kernel
holds for each declared parameter, in the kernel's own rendering.

Then read the operator that owns the device. A screen appears in
the display operator's slice, an output in the audio operator's, a
radio in the Bluetooth operator's:

    kubectl get resourceslices

The entry in `status.hardware.unclaimed` is gone once a driver binds
the device.

## 7. Write the list into the manifest

A live patch changes the cluster and nothing else. Copy the final
`spec.modules` and `spec.moduleParameters` into the machine's
manifest in your deployment directory, so the next install stick
and the next reinstall start from the same list. On a fleet run from
git, the commit is the edit, and the machine converges to it.
