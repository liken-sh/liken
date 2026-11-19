# Two waits have no bound after SIGKILL

Open bug, with an open design question. When k3s is in uninterruptible
sleep, such as on hung storage, SIGKILL cannot end it, and init waits
for its exit with no time limit. A requested reboot then never reaches
the reboot syscall, and the machine stays hung until a person cuts its
power. A unit test reproduced each wait on 2026-11-19.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`stopK3s` (`init/supervisor.go:390`) receives on the death channel with
no timeout after the kill (`init/supervisor.go:399`). `stopSupplicant`
(`init/wireless.go:470`) has the same receive (`init/wireless.go:478`).

`stopK3s` runs before `rebootMachine`, while the reaper still runs.
The hang it can meet is a k3s in uninterruptible sleep, such as on hung
storage, that SIGKILL cannot end, and then a requested reboot never
reaches the reboot syscall. `stopSupplicants` already bounds each
supplicant's stop, so only `endSupplicant` (`init/wireless.go:288`) at
bring-up waits with no bound.

Two tests ran `stopK3s` and `stopSupplicant` in a `synctest` bubble,
each with a process on which a signal has no effect and a death
channel that never receives, as for a process in uninterruptible
sleep. For `stopK3s` the process had already exited, and for
`stopSupplicant` its signal function did nothing. Each test waited an hour of the bubble's clock and asserted
that the stop had returned. They failed with:

```
stopK3s still waits an hour after SIGKILL for a death that the reaper never reports, want it to return
stopSupplicant still waits an hour after SIGKILL for a death that the reaper never reports, want it to return
```

The tests are not in the tree, because a failing test cannot land on
`main`.

## The fix

Both receives get a deadline, and a miss is printed and reported. A
missed wait on the restart path must not start a second k3s beside the
first, so the deadline needs a decision on what a miss does there.

The open problem
[the proving watchdog blocks its own shutdown](the-proving-watchdog-blocks-its-own-shutdown.md)
depends on this bound: its fix sends the watchdog's reboot through
`stopK3s`.

## The design question

This needs the maintainer's decision before the fix is built.

**What a missed wait does on the restart path.** When `stopK3s`
reaches its new deadline after SIGKILL, a k3s in uninterruptible sleep
may still run. On the reboot path, the miss can go on to the reboot
syscall. On the restart path, `superviseK3s` must not start a second
k3s beside the first, and nothing decides yet what it does instead.
It can leave k3s stopped and report the stuck process in the
machine's status, so the machine stays up without k3s until a person
reboots it. Or it can reboot the machine, as the reboot path does.
