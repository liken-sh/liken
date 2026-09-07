// equipment-operator drives the A/V receivers of a liken cluster and
// reports what each one says. It reaches each receiver over the
// network, applies the session a Player holds on it, and owns the
// room's level while that session stands.
package main

import "os"

// The operator's own environment. Each value is one the operator
// cannot derive, so the Deployment states it and this program reads
// it here.
const (
	// POD_NAMESPACE is the namespace the Deployment runs in. Nothing reads
	// it until the Service front lands.
	podNamespaceVariable = "POD_NAMESPACE"

	// EQUIPMENT_BUS_ADDRESS is the broker a session's volume topic is read
	// from, as host:port.
	busAddressVariable = "EQUIPMENT_BUS_ADDRESS"
)

// settings is the whole of the operator's configuration.
type settings struct {
	namespace  string
	busAddress string
}

// readSettings takes the configuration from the environment alone.
// An unset variable reads as empty, and the caller decides what an
// empty value means, so a missing setting never stops the read.
func readSettings() settings {
	return settings{
		namespace:  os.Getenv(podNamespaceVariable),
		busAddress: os.Getenv(busAddressVariable),
	}
}

func main() {
	operate()
}
