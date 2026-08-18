---
title: audio.liken.sh
---

# audio.liken.sh

The audio operator publishes each physical audio output of a
[`liken`](https://liken.sh/) machine as a Kubernetes DRA device, under
the device class `audio.liken.sh`. A pod claims one output, a
monitor's speakers or the analog jack, and receives the PipeWire
socket and the name of the sink its streams must reach. The operator
runs PipeWire and WirePlumber in its own pod, so the machine's system
image carries no sound server.

The operator's manual will publish here.

* [The repository](https://github.com/liken-sh/audio-operator)
* [The `liken` manual](https://liken.sh/docs/)
