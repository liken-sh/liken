A `PairingRequest` is one pairing window on one adapter. Create it
to open the window, watch `status.seen` for the controller you put
in pairing mode, and approve that controller by writing its address
into `spec.device`. An empty `spec.device` never pairs anything.
The resource is namespaced, so RBAC can grant the right to pair in
one namespace without a shell on any node. A finished request
reports `Paired` or `Expired` and is collected after its TTL, and
the [Peripheral](/docs/reference/peripherals/) it produced records
the request's name. [Pair a controller](/docs/guides/pair-a-controller/)
gives the steps.
