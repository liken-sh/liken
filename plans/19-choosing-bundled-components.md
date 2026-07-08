# Choosing the bundled components

Milestone 19 — Not started

The static k3s config disables traefik, servicelb, and metrics-server
on principle: anything beyond the control plane should be a declared,
visible workload. Some deployments reasonably want k3s's bundled
versions instead, and that choice should be theirs to make rather
than hardcoded in the image. Milestone 16 also showed the disable
list has cluster-wide effect: a joining server that disables a
component submits the job that removes the packaged component from
the whole cluster.

The capability: the Cluster document declares which bundled
components run, and init renders the disable list into the boot
drop-in. This is a fact every node must agree on, which is the
Cluster document's job.
