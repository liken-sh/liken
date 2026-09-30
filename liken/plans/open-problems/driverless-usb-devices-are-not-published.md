# Driverless USB devices are not published

Open design problem. `liken` publishes a device in the node's
`ResourceSlice` only when a kernel driver has bound it. Some USB
devices never have a kernel driver, because their vendor supplies a
userspace driver that uses libusb. No pod can claim these devices, and
no edit to `spec.modules` changes that.

## What happens

`inventoryDevices` in
[`dra.go`](../../machine-operator/dra.go) skips every device whose
`Driver` is empty. The comment above the function gives the model:
a device with no driver goes to the unclaimed report in `Machine`
status, and a person moves it into the inventory when they declare its
module in `spec.modules`.

That model fits a device that waits for a module. It does not fit a
device that is complete with no module. The kernel enumerates such a
device, creates its interfaces in sysfs, and gives the device a usbfs
node, `/dev/bus/usb/<busnum>/<devnum>`. A program that uses libusb
reads sysfs to find the device and then opens the usbfs node. It needs
nothing more from the kernel.

The ZWO ASI astronomy cameras are one example. ZWO ships no Linux
kernel driver, and its `libASICamera2` SDK reaches the camera through
libusb ([ZWO software page](https://us.zwoastro.com/pages/software)).
Their interfaces have no driver, so the inventory skips them. The
unclaimed report omits them too, because
[`unclaimed.go`](../../hardware/unclaimed.go) lists a device only when
a module in the kernel build could drive it. The camera is present in
sysfs and absent from both reports. Other devices with the same shape
include many software-defined radios, some UPS models, and other
vendors' cameras and instruments.

The same devices already work in one state. When a libusb program
detaches a kernel driver from an interface,
[`cdi.go`](../../machine-operator/cdi.go) rewrites the claim's CDI
specification to the usbfs node alone. So `liken` already delivers a
claim that consists of only a usbfs node. What is missing is a device
in the slice for the scheduler to allocate.

## The principle for a remedy

The inventory projects the kernel's view of the hardware into the
`ResourceSlice`. It does not classify hardware by vendor or product,
and it does not guess what a workload will do with a device. A remedy
should keep that property. A table of vendor and product IDs in
`liken`, or a special case for each kind of device, would make the
operating system responsible for knowing every product that a
workload can drive.

## Candidate directions

None of these is a selected design.

- **Publish every driverless USB interface with its usbfs node.**
  This is the kernel's own view: the interface exists, it has no
  driver, and the kernel gives its device one node. The device
  publishes as exclusive, because usbfs carries raw transfers to every
  endpoint and has no arbitration between two programs. USB is the
  only bus where the kernel gives a driverless device a node, so the
  rule needs no product knowledge. The cost is that `spec.modules` is
  no longer the only gate into the inventory. A device that waits for
  a module also publishes as a raw usbfs device until the module
  loads, and then its published shape changes.
- **Declare userspace devices on the `Machine`.** A second list beside
  `spec.modules` names USB devices by vendor and product, and each
  device on it publishes as exclusive with its usbfs node. The person
  keeps the gate. The cost is a second declaration and a vendor table
  in each fleet's manifests.
- **Leave the inventory unchanged.** An extension operator publishes
  driverless devices in a slice of its own, for the hardware it
  serves. The cost is that each such operator walks sysfs again and
  writes its own CDI specifications, which duplicates the node's
  delivery code.

The first direction matches the principle above most closely.

## Verification needed

- Plug in a USB device that has no kernel driver, for example a ZWO
  ASI camera, and confirm what sysfs shows: the interfaces, the empty
  `driver` link, and the usbfs node.
- Confirm that `hardware.InspectDelivery` returns the usbfs node for
  an interface with no driver.
- While a libusb program holds an interface, sysfs can show `usbfs` as
  the interface's driver. Confirm what the walk reads in that state,
  and whether the device stays in the slice while its claim is held.
- Run the vendor's SDK in a pod under a claim on the published device,
  with no privilege and no host path.
