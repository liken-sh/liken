package machine

// The kmsg wire contract: the syslog facts that let init and the log
// relays agree about records in the kernel's log buffer.
//
// Records written through /dev/kmsg carry a priority value packing
// two facts, facility<<3 | severity, syslog's encoding since RFC
// 3164. The facility is what separates the two programs sharing the
// buffer: the kernel's own records carry facility 0, and anything
// userspace writes through /dev/kmsg carries facility 1. init writes
// its log lines there (init/console.go), and the liken-logs relays
// read them back out (logs/kmsg.go); each side lives in its own
// binary, so the numbers they must agree on live here, in the one
// package both import.

const (
	// FacilityKernel marks records the kernel itself printed;
	// FacilityUser marks records userspace (on liken, only init)
	// wrote through /dev/kmsg. One relay ships one facility, so
	// "which program said this" is a field, not a guess.
	FacilityKernel = 0
	FacilityUser   = 1

	// The two severities init speaks: ordinary narration is info,
	// and anything written to stderr is warning. The numbers are RFC
	// 5424's, where lower is more severe.
	SeverityWarning = 4
	SeverityInfo    = 6
)

// SeverityNames are the syslog severity words, indexed by numeric
// severity (RFC 5424's table). The relays' envelopes carry words
// rather than numbers so a human reading raw pod logs doesn't need
// the table.
var SeverityNames = [8]string{
	"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug",
}
