# The agent shuts down slowly

A busy Corrosion agent took more than 30 s to exit on `SIGTERM` twice
in the proof of concept, and its harness had to send `SIGKILL`. At
rest it exited in 5 to 10 s. Kubernetes gives a pod 30 s by default
before it kills what is left.

Plans 03 and 06 set a longer grace period on every pod that runs an
agent, which is enough to stop the kill and not enough to explain
the delay. What the agent is doing in that time, whether a kill
mid-sync leaves its file in a state the next start recovers from, and
whether the delay grows with the cluster, are unmeasured. A drill
that kills a screen's pod mid-sync and times the next start is the
next step, and plan 09's reboot step is the first look.
