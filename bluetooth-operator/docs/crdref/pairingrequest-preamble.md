A `PairingRequest` opens one pairing window on one adapter. Create it
to open the window, watch `status.seen` for the controller that you
put in pairing mode, and approve that controller by writing its
address into `spec.device`. The operator pairs no device while
`spec.device` is empty. A `PairingRequest` is namespaced, so RBAC can
grant the right to pair in one namespace, and nobody needs a shell on
a node. A finished request reports `Paired` or `Expired`, and the
operator deletes it `spec.ttlSecondsAfterFinished` seconds later. The
[Peripheral](/docs/reference/peripherals/) that the request created
records the request's name in `status.bond.request`. [Pair a controller](/docs/guides/pair-a-controller/)
gives the steps.
