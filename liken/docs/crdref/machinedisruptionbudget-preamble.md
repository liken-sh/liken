A `MachineDisruptionBudget` limits how many of the machines it
selects can be out of service at the same time. The budget named
`default` covers the whole fleet. Add a budget with a selector to
give a group of machines a stricter limit. `kubectl get mdb` lists
each budget and how many more machines it allows out now. [How
`liken` works](/docs/concepts/how-liken-works/#the-reboot-turn)
explains how the budgets decide each reboot turn, and
[`Disruption`](/docs/reference/disruption/) describes the requests
that wait for them.
