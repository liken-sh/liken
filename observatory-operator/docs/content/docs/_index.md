---
title: Manual
---

# Manual

`observatory-operator` runs an observatory's equipment for each
reservation of a telescope. To go from an empty cluster to a telescope
that KStars drives, follow the guides in order:

1. [Install the operator](/docs/guides/install/), and run the example
   observatory of simulators.
2. [Describe your equipment](/docs/guides/describe-your-equipment/):
   the site, each telescope, its optics, and each device with its INDI
   driver.
3. [Connect USB equipment](/docs/guides/connect-usb-equipment/), so
   each driver reaches its hardware.
4. [Reserve a telescope](/docs/guides/reserve-a-telescope/), and
   connect KStars and Ekos to it.

Then add what your site needs.
[Automate the equipment](/docs/guides/automate-the-equipment/) covers
the dome, the dust cap, the camera's cooler, power, and the weather.
[Set up a portable telescope](/docs/guides/set-up-a-portable-telescope/)
covers a telescope that someone carries out each night, and
[Guide with PHD2](/docs/guides/guide-with-phd2/) covers autoguiding.
[Troubleshoot](/docs/guides/troubleshoot/) starts from the step that
failed or waits.

The concepts explain what the guides do: [the
resources](/docs/concepts/resources/) and their tree, [how a
reservation runs](/docs/concepts/how-a-reservation-runs/) from step to
step, the [FITS keywords](/docs/concepts/fits-keywords/) that your
equipment adds to each frame, [procedures](/docs/concepts/procedures/)
and triggers, [the dome and mount locks](/docs/concepts/dome-and-mount-locks/),
and [the guider](/docs/concepts/the-guider/). The reference lists the
drivers and their images, what each status field means, the objects
the operator creates, the Prometheus metrics, and every field of the
20 resources.

Targets, plate solving, and where the frames go are
`astrophotography-operator`'s. Its
[manual](https://liken.sh/astrophotography/) covers them.
