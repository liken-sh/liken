// equipment-operator controls the A/V receivers and TVs of a liken
// cluster. It connects to each receiver over the network, reports the
// receiver's state in the Receiver status, and applies the settings and
// the session that media-operator writes for a Player. The cec
// subcommand runs the workload that controls a TV through a CEC
// adapter.
package main

import (
	"fmt"
	"os"
)

// version is set by the Dockerfile with -ldflags "-X main.version=...".
// A build without the flag, such as go test, reports dev.
var version = "dev"

// The environment variables that the Deployment sets for the operator.
const (
	// POD_NAMESPACE is the namespace of the leader election Lease
	// (leader.go).
	podNamespaceVariable = "POD_NAMESPACE"

	// POD_NAME is the prefix of this replica's identity in the leader
	// election.
	podNameVariable = "POD_NAME"

	// EQUIPMENT_METRICS_ADDRESS is the host:port that serves /metrics.
	// When it is empty, the operator serves no metrics.
	metricsAddressVariable = "EQUIPMENT_METRICS_ADDRESS"

	// EQUIPMENT_NETWORK_DISCOVERY is on or off, and unset means on.
	// Network discovery finds the WiiM amps on the LAN and creates a
	// Receiver for each amp whose UUID no Receiver's spec.wiim.uuid
	// has, so the operator starts to control it. Turn it off when the cluster shares its LAN
	// with amps that it must not control.
	networkDiscoveryVariable = "EQUIPMENT_NETWORK_DISCOVERY"
)

// settings is the operator's configuration, read from the environment.
type settings struct {
	namespace      string
	pod            string
	metricsAddress string
	// networkDiscoveryOff is false by default, so discovery is on.
	networkDiscoveryOff bool
	// dial and listen replace the operator's TCP connections to the
	// receivers and its listener for WiiM events. Only tests set them.
	dial   dialFunc
	listen listenFunc
}

// readSettings reads the configuration from the environment. It
// returns an error for an EQUIPMENT_NETWORK_DISCOVERY other than on or
// off, because a misspelled off would leave discovery on and the
// operator would take control of amps that it was meant to leave alone.
func readSettings() (settings, error) {
	config := settings{
		namespace:      os.Getenv(podNamespaceVariable),
		pod:            os.Getenv(podNameVariable),
		metricsAddress: os.Getenv(metricsAddressVariable),
	}
	switch value := os.Getenv(networkDiscoveryVariable); value {
	case "", "on":
	case "off":
		config.networkDiscoveryOff = true
	default:
		return settings{}, fmt.Errorf("%s must be \"on\" or \"off\", and is %q", networkDiscoveryVariable, value)
	}
	return config, nil
}

// The image holds two builds of this program. The Deployment runs
// operatorBinary. The cec DaemonSet runs nodeBinary, built with the node
// tag, which leaves out leader election (leader_node.go).
const (
	operatorBinary = "/equipment-operator"
	nodeBinary     = "/equipment-operator-node"
)

// main runs the operator, or the node workload for one CEC adapter
// when the first argument is cec.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "cec" {
		runCEC()
		return
	}
	operate()
}
