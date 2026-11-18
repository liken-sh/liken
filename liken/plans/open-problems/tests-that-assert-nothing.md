# Tests that assert nothing

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Some tests in init run code and check no result, so they fail only
when the code panics. Some of them also read or write the machine that
runs the test, which the testing rules forbid.

## The evidence

- `TestConnectionReportPrintsEachMethod` (`init/network_test.go:97`)
  calls `connection.report` for a DHCP and a static connection. Its
  comment says that it checks no output.
- `TestReportBlockDevicesPrintsDisks` (`init/disks_test.go:214`) and
  `TestReportBlockDevicesWithNoDisks` (`init/disks_test.go:301`) call
  `reportBlockDevices` and check nothing.
- `TestShiftLogContinuesAfterARenameFailure`
  (`init/logrotate_test.go:196`) calls `shiftLog` on a read-only
  directory and checks nothing. Its name says that something continues
  after the failure.
- `TestReportFirmwareUEFI` and `TestReportFirmwareBIOS`
  (`init/efi_test.go:183` and `205`) call `reportFirmware` and then
  check `firmwareFacts`, so no test checks the lines that
  `reportFirmware` prints.
- `TestApplySysctlsSkipsFailuresAndContinues`
  (`init/system_test.go:43`) does not set `sysctlDir`, so it writes
  under the real `/proc/sys`, and it checks nothing.
- `TestWorldReportReadsTheWholeMachine` (`init/worldreport_test.go:10`)
  calls `worldReport`, which reads the real `/proc/self/mounts`
  (`init/worldreport.go:89`) and prints it to the test log. It checks
  nothing. On a workstation the log then holds that workstation's
  mounts.

The `testing` skill under `.agents/skills` says that a test asserts
what a person or a peer observes, and the root `AGENTS.md` says that
tests run on the Go toolchain alone.

## What would confirm it

Make each printing function under test return at once, and run the
tests. Each of these tests still passes.

## What a fix has to decide

What each test checks. A report function can take an `io.Writer`
that the test reads. `worldReport` and `applySysctls` need their
paths as variables, as `sysctlDir` already is for the other sysctl
tests.
