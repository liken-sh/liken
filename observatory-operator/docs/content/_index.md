---
title: observatory-operator
---

# `observatory-operator`

`observatory-operator` runs an observatory's equipment on a
[`liken`](https://liken.sh/docs/) cluster. You describe your mount,
cameras, focusers, dome, and the rest of your equipment as Kubernetes
resources. When you reserve a telescope, the operator starts an
[INDI](https://indilib.org/) driver for each device, connects them,
and runs the steps you declared for the start of the night, such as
opening the dome and cooling the camera. You drive the telescope from
KStars and Ekos on your desktop, as you would with any INDI server.
When the reservation ends, the operator runs the steps you declared
for the end, such as parking the mount and warming the camera, and
tells you when the equipment is safe to power off.

Things you can do with it:

* Reserve a telescope for a night, and connect KStars and Ekos to its
  INDI server.
* Open a roll-off roof only while the weather station reports safe
  weather, and close it when the weather turns.
* Cool the camera at the start of the night, and warm it before the
  cooler goes off.
* Keep a portable telescope off until someone carries it out and
  confirms that it is set up, and ask them to cover it at the end of
  the night.
* Switch each device's power on and off through a power box.
* Run PHD2 for the guide camera and the mount, and read the guiding
  error from the cluster.
* Share one dome and one weather station between two telescopes.

The operator does not take pictures, plate-solve, or guide on its own.
It starts PHD2 and connects it to your guide camera and mount, and you
run the session from KStars, or `astrophotography-operator` runs it.

The operator's tests and the drills on a test cluster run against the
INDI simulators. Real hardware connects through the same resources, but no
drill has run on real equipment yet, and a QHY camera that loads its
firmware through udev rules on the host does not work.
[Connect USB equipment](/docs/guides/connect-usb-equipment/) gives
the details.

Start here:

* [Install the operator](/docs/guides/install/) with an observatory of
  simulators, so you can see every part work before you connect any
  hardware.
* [Describe your equipment](/docs/guides/describe-your-equipment/), and
  [connect its USB devices](/docs/guides/connect-usb-equipment/).
* [Reserve a telescope](/docs/guides/reserve-a-telescope/) and drive
  it from KStars.
* [Automate the equipment](/docs/guides/automate-the-equipment/) for
  the start and the end of each night, and for bad weather.
* [Troubleshoot](/docs/guides/troubleshoot/) a reservation that fails
  or waits.

`observatory-operator` is one of the extension operators for
[running an observatory](https://liken.sh/docs/concepts/running-an-observatory/).
It runs the equipment, and
[`astrophotography-operator`](https://liken.sh/astrophotography/)
reserves a telescope from it to image the targets you declare.

* [The source](https://github.com/liken-sh/liken/tree/main/observatory-operator)
* [The design documents](https://github.com/liken-sh/liken/tree/main/observatory-operator/plans)
* [The `liken` manual](https://liken.sh/docs/)
