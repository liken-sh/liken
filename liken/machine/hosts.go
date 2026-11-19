package machine

// Init writes /etc/hosts at boot, and machine-operator rewrites it
// when it differs from what HostsFile returns. Both render it here, so
// for the same entries they write the same text, and machine-operator
// does not rewrite the file that init wrote.

import (
	"fmt"
	"strings"
)

// HostsFile returns the text of /etc/hosts: fixed lines for
// localhost and for the machine's own name at 127.0.1.1, then one
// line for each entry, in the caller's order, so a person can compare
// the file with the manifest. A resolver uses the first match, so an
// entry can add a name but cannot override localhost or the
// machine's own name.
func HostsFile(hostname string, entries []HostEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "127.0.0.1 localhost\n::1 localhost\n127.0.1.1 %s\n", hostname)
	for _, entry := range entries {
		fmt.Fprintf(&b, "%s %s\n", entry.Address, strings.Join(entry.Names, " "))
	}
	return b.String()
}
