A `Disruption` is one request to take one machine out of service: a
reboot, or a restart of k3s. `machine-operator` files one for the
changes it stages, and [`liken
request-reboot`](/docs/reference/cli/#liken-request-reboot) files one
for a person. `kubectl get disruptions` lists every request, who
asked for it, and whether it is approved and granted. [How `liken`
works](/docs/concepts/how-liken-works/#the-reboot-turn) explains how
a request becomes a reboot turn, and a
[`MachineDisruptionBudget`](/docs/reference/machinedisruptionbudget/)
sets how many machines take a turn at the same time.
