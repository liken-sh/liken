# A CEC version 1.1 device reads as unknown

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`cec/message.go` defines `VersionUnknown` as 0. In the HDMI-CEC
specification, the CEC Version operand 0x00 means version 1.1, and
0x01 to 0x03 mean 1.2, 1.2a, and 1.3. `Directory` records the operand
of a CEC Version reply as it is (`cec/directory.go`), so a device that
reports 1.1 has the version `VersionUnknown`. The scan in
`cec/scan.go` treats the version as known only when it is not
`VersionUnknown`, so it sends Get CEC Version to that device again on
each scan. `Version.String` returns an empty string for 0 to 3, so the
status shows no version for any device before 1.3a.

## Evidence

- `VersionUnknown Version = 0` beside `Version13a = 4`,
  `Version14 = 5`, and `Version20 = 6`.
- The `known` function of the Get CEC Version query in `cec/scan.go`.

## What would confirm it

Check the CEC Version operand table in the HDMI 1.3a CEC supplement.
Then a test that feeds the `Directory` a CEC Version reply of 0x00 and
runs a scan on the fake bus in `cec/cectest`.

## What a fix has to decide

- Which value marks an unknown version, such as a separate field or a
  value outside 0 to 6.
- Whether `Version.String` names 1.1 to 1.3.
