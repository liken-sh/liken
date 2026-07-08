# Node labels on the Machine

Milestone 21 — Not started

Workloads schedule on node labels: which machine has the GPU, which
one is on battery-backed power, which may run the noisy batch jobs.
Today the OS applies exactly one label (liken.sh/machine, from the
static k3s config), and any further labeling happens through kubectl,
outside the Machine document, so a reinstalled machine comes back
without it.

The capability: labels declared on the Machine spec, rendered as k3s
node-label entries in the boot drop-in so a machine registers with
them, and reconciled by the operator afterward. The kubelet applies
registration labels but never removes stale ones, so drift correction
belongs to the same operator pass that already reconciles sysctls.
