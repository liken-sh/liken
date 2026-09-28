package machine

// This file defines the kmsg wire contract: the syslog facts that
// let init and the log relays agree on records in the kernel's log
// buffer.
//
// A record written through /dev/kmsg carries a priority value. The
// priority value packs two facts together: facility<<3 | severity.
// This is syslog's encoding, defined since RFC 3164. The facility
// value separates the two programs that share the buffer. The
// kernel's own records carry facility 0. Anything userspace writes
// through /dev/kmsg carries facility 1. init writes its log lines
// there, in init/console.go. The liken-logs relays read the log
// lines back out, in logs/kmsg.go. Each side lives in its own
// binary. So the numbers both sides must agree on live here, in the
// one package both binaries import.

const (
	// FacilityKernel marks records that the kernel itself printed.
	// FacilityUser marks records that userspace wrote through
	// /dev/kmsg (on liken, only init writes these). One relay ships
	// one facility. So a reader finds "which program said this" in a
	// field, not by a guess.
	FacilityKernel = 0
	FacilityUser   = 1

	// init uses two severities. Ordinary narration uses info.
	// Anything written to stderr uses warning. The numbers come from
	// RFC 5424, where a lower number means a more severe record.
	SeverityWarning = 4
	SeverityInfo    = 6
)

// SeverityNames holds the syslog severity words, indexed by numeric
// severity from the RFC 5424 table. The relays' envelopes carry
// words instead of numbers. So a person reading raw pod logs does
// not need the table.
var SeverityNames = [8]string{
	"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug",
}
