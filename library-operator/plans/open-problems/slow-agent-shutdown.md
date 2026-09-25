# Slow agent shutdown

A busy Corrosion agent took more than 30 s to exit on `SIGTERM` twice in
the proof of concept, and its harness sent `SIGKILL`. At rest it exited
in 5 to 10 s. Kubernetes gives a pod 30 s by default before it kills
what is left.

Plans 03 and 06 set a longer grace period on every pod that runs an
agent. That prevents the kill, but it does not explain the delay. Three
things are not measured: what the agent does in that time, whether a
kill during a sync leaves its file in a state that the next start
recovers from, and whether the delay grows with the cluster. The next
step is a drill that kills a screen's pod during a sync and times the
next start. Plan 09's reboot step is the first test of this.
