package machine

// init writes its log lines to /dev/kmsg, and the liken-logs relays
// read them back from the kernel's log buffer. They are separate
// binaries, so the numbers they must agree on are defined in this
// package, which both import.
//
// A record's priority is facility<<3 | severity (RFC 3164). The
// kernel's own records have facility 0, and records that userspace
// writes through /dev/kmsg, such as init's, have facility 1. Each
// relay ships the records of one facility.

const (
	FacilityKernel = 0
	FacilityUser   = 1

	// init writes its stdout at SeverityInfo and its stderr at
	// SeverityWarning. These are RFC 5424 severity numbers, where a
	// lower number is more severe.
	SeverityWarning = 4
	SeverityInfo    = 6
)

// SeverityNames maps an RFC 5424 severity number to its name. The
// relays write the name into each log line, so a person who reads the
// pod logs reads "warning" and not 4.
var SeverityNames = [8]string{
	"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug",
}
