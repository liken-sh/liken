# observatory-operator

`observatory-operator` will be the hardware control layer of an
observatory on a [`liken`](https://liken.sh/) cluster, under the API
group `observatory.liken.sh`. It will run each INDI device in its own
pod with its own DRA claim, serve every device on one INDI server, run
the PHD2 guider, and configure each device when it appears. KStars, or
`astrophotography-operator`, drives the observatory through that server.

Nothing is built yet. [`plans/00-design.md`](plans/00-design.md) is the
design, and [root plan 74](../plans/74-astrophotography.md) holds the
architecture and the tests behind it.
