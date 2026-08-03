# Claiming unknown machines

Open problem. `liken.machine=` identifies a machine that somebody
declared before it booted. Nothing identifies the machine that nobody
declared.

## The shape of an answer

A Machine template on the Cluster would let an unknown node claim an
identity on its first boot. The node would take its name from a
hardware fact, probably its MAC address, because the network already
forces that address to be unique. It would take its address from a
pool, probably by ARP-probe claiming, in the same way that storage
claiming works: probe reality, take what is free, and refuse an
ambiguous case.

## What it waits on

Milestones [50](../50-netboot-for-a-declared-machine.md) and
[51](../51-enrollment-over-the-network.md) propose the supervised half.
The unknown machine presents itself over the network, and a person
applies the proposal. The template is the unsupervised half, and it
waits until the supervised flow is proven.
