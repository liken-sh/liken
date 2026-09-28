// equipment-operator drives the A/V receivers of a liken cluster and
// reports what each one says. It reaches each receiver over the
// network, applies the session a Player holds on it, and owns the
// room's level while that session stands.
package main

import (
	"fmt"
	"os"
)

// version is the release this binary was built from. The Dockerfile
// sets it with -ldflags "-X main.version=...", and a build with no
// such flag, such as go test, reports dev.
var version = "dev"

// The operator's own environment. Each value is one the operator
// cannot derive, so the Deployment states it and this program reads
// it here.
const (
	// POD_NAMESPACE is the namespace the Deployment runs in, which holds
	// the Lease its copies compete for (leader.go).
	podNamespaceVariable = "POD_NAMESPACE"

	// POD_NAME is the pod's own name, the start of the identity the
	// operator holds the Lease under.
	podNameVariable = "POD_NAME"

	// EQUIPMENT_BUS_ADDRESS is the broker a session's volume topic is read
	// from, as host:port.
	busAddressVariable = "EQUIPMENT_BUS_ADDRESS"

	// EQUIPMENT_METRICS_ADDRESS is where /metrics listens, as host:port.
	// liken/plans/65-prometheus-metrics.md gives every process port 9200,
	// since this operator runs on the cluster network. An empty value
	// turns the listener off.
	metricsAddressVariable = "EQUIPMENT_METRICS_ADDRESS"

	// EQUIPMENT_NETWORK_DISCOVERY is on or off, and unset reads as on.
	// Network discovery searches the LAN for WiiM amps and creates a
	// Receiver for each one that no Receiver names, and the operator then
	// drives that amp. A cluster that shares its LAN with amps it must not
	// drive, such as a test cluster beside a home's own equipment, turns
	// it off.
	networkDiscoveryVariable = "EQUIPMENT_NETWORK_DISCOVERY"
)

// settings is the whole of the operator's configuration.
type settings struct {
	namespace      string
	pod            string
	busAddress     string
	metricsAddress string
	// networkDiscoveryOff is true when the Deployment turned network
	// discovery off. The zero value is on, the default.
	networkDiscoveryOff bool
}

// readSettings takes the configuration from the environment alone.
// An unset variable reads as empty, and the caller decides what an
// empty value means, so a missing setting never stops the read. The
// one value it refuses is a network discovery value other than on or
// off: a misspelled off would leave discovery on, and the operator
// would drive the amps the cluster owner meant to leave alone.
func readSettings() (settings, error) {
	config := settings{
		namespace:      os.Getenv(podNamespaceVariable),
		pod:            os.Getenv(podNameVariable),
		busAddress:     os.Getenv(busAddressVariable),
		metricsAddress: os.Getenv(metricsAddressVariable),
	}
	switch value := os.Getenv(networkDiscoveryVariable); value {
	case "", "on":
	case "off":
		config.networkDiscoveryOff = true
	default:
		return settings{}, fmt.Errorf("%s is %q; it takes on or off", networkDiscoveryVariable, value)
	}
	return config, nil
}

// The image holds two builds of this program, so the two halves stay
// at one version. operatorBinary is the full build, which the
// Deployment runs. nodeBinary is the build with the tag node, which
// the cec DaemonSet runs with the argument cec. It leaves out the
// leader election that only the Deployment needs (leader_node.go).
const (
	operatorBinary = "/equipment-operator"
	nodeBinary     = "/equipment-operator-node"
)

// main runs the Deployment's operator, or with the argument cec, the
// node workload that holds one CEC adapter.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "cec" {
		runCEC()
		return
	}
	operate()
}
