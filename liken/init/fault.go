package main

// fault makes a release fail on purpose, so a drill can check that the
// machine falls back to the proven slot. The release build sets it at
// link time with -X, from `make release VERSION=x FAULT=...`. Every
// real release leaves it empty. The values are:
//
//	panic      init panics at startup. When PID 1 exits the kernel
//	           panics, and the panic=10 kernel argument reboots the
//	           machine ten seconds later. The firmware already used
//	           its one-shot BootNext, so it boots the proven slot from
//	           BootOrder. No liken code takes part in this recovery.
//
//	wedge-k3s  init starts everything except k3s, so the node never
//	           joins and the release is never promoted. Nothing panics
//	           and nothing reboots until the proving watchdog reboots
//	           onto the proven slot after provingTimeout.
var fault = ""
