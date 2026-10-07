---
title: observatory.liken.sh
---

`observatory-operator` runs a telescope's hardware on a
[`liken`](https://liken.sh/docs/) cluster. You describe your mount,
cameras, focusers, dome, and the rest of your equipment as Kubernetes
resources. When you reserve a telescope, the operator starts an
[INDI](https://indilib.org/) driver for each device, connects them,
and runs the steps you declared for the night: unpark the dome, cool
the camera, open the dust cap. When the reservation ends, it parks
everything, warms the camera, and tells you when the equipment is safe
to power off. You drive the telescope from KStars and Ekos on your
desktop, as you would with any INDI server.

The operator does not take pictures, plate-solve, or guide on its own.
It starts PHD2 for guiding and connects it to your guide camera and
mount, and you run the session from KStars.

The operator is under development, and it has no release yet. Its
tests and the drills on a test cluster run against the INDI
simulators. Real hardware connects through the same resources, but no
drill has run on real equipment yet, and some cameras cannot reach a
pod at all. [Connect USB equipment](docs/guides/connect-usb-equipment/)
gives the details.

Start with the [manual](docs/). The design and the plans are in the
[`observatory-operator` directory](https://github.com/liken-sh/liken/tree/main/observatory-operator)
of the `liken` repository.
