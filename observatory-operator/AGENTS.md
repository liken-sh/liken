# Working on observatory-operator

`observatory-operator` is a Go Kubernetes operator. `README.md` says
what it is for and describes its kinds and its Go packages.

## How it runs

The operator is one `Deployment` in `liken-system`. Only the pod that
has its `Lease` acts. Two copies that acted at once would both run the
same reservation's steps. The operator creates each observatory's
pods, `Service`s, claims, and `Job`s in the namespace of the
observatory's resources, so that namespace's quota and RBAC apply to
them.

While a `Reservation` is active, each device of its telescope runs in
its own pod and serves its driver with `socat`. Each `Telescope` has
one `indiserver` pod. The `Observatory` has one more, for the devices
that no telescope owns. In a server's pod, `indi-shim` runs
`indiserver` and connects to each device pod through the pod's
`Service`. The operator writes the server's list of devices as an
annotation on the server's pod. The pod reads the annotation through
a downward API volume, so a device can join or leave a running server
without a restart. The guider's pod runs PHD2 beside a headless
`weston`, and PHD2 connects to the telescope's server as a client.

The operator has one INDI client for each server and one PHD2 client
for each guider. With them, it configures each device and runs each
resource's procedures. It also copies the park states from one server
to another, so that INDI's drivers enforce the dome and mount locks.
It writes what it reads into the status of each resource. The
reservation's holder, KStars or `astrophotography-operator`, connects
to the same INDI server and to PHD2's event server.

## Design

The operator records the progress of a reservation in the
reservation's `status.steps`, not in memory. After a restart, a step
can run again. Before it changes anything, it reads what the cluster
and the devices report, so it sends a change only when the change is
still needed. While a reservation's session uses a server, the server
runs only the devices that the session started. So a device moves
from one telescope to another only between sessions.

`astrophotography-operator` depends on this component. This component
does not depend on it. `astrophotography-operator` imports the
`observatory`, `indi`, and `phd2` packages, so those packages import
only the standard library and `kubernetes/conditions`. A change to an
exported name or a behavior of one of those packages also runs
`astrophotography-operator`'s tests.

The contract between the two operators is a `Reservation` whose
holder is `astrophotography-operator`. For that reservation, this
operator publishes the endpoints, the session's devices, and the
outbox. `astrophotography-operator` reads them, and drives the
equipment through those endpoints. Targets and nights belong to the
holder, and this operator records nothing about them.

The INDI images are built from `indi/` at the top of the repository.
A change to any file there changes the recipe of every INDI image.
So the bare drivers, which capture the standard INDI properties, are
in this component, in `indi/standard/bare/`. The base in `deploy/`
does not include the `Firmware`s for the bare QHY chips, because the
`Firmware` CRD comes from `firmware-operator`.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

- Load the `observatory-schema` skill under `.agents/skills` before
  you change a kind, a CRD, a template, or a fields file, and after a
  bump of the INDI images.
- Some files are generated. Each one has a command that writes it
  again, and a test that fails when the file is stale:
  - The CRDs of the kinds with procedures, and
    `observatory/generated_*.go`: `make schema`.
  - `drivers/generated.go`: `make drivers`.
  - `deploy/firmware/qhy.yaml`: `make firmware`.

  `make -C docs generate skills` writes the manual's reference pages
  and `skills/`.
- A mount reports its position several times a second. So
  `cost_test.go` fails when an INDI or PHD2 reading wakes anything
  more than the status writer, the lock relay, and a wait for a value.
- Most tests use `examples/simulators.yaml` as their inventory, so a
  change to the example changes those tests.
