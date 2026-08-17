// bondfetch writes an adapter's stored bonds into the directory
// bluetoothd reads, and then exits.
//
// It is a plain init container. The pod's bonds live in a Kubernetes
// Secret named after the adapter's own Bluetooth address (see the
// bonds package), and bluetoothd reads them from a directory tree. The
// pod's bluetoothd container mounts an emptyDir at that tree, this
// program fills it before bluetoothd starts, and the operator writes
// the Secret back whenever the tree changes.
//
// The address comes from the kernel, because bluetoothd is not running
// yet and no other source in the pod carries the address of the radio
// the kubelet delivered. The pod runs in the host's network namespace,
// which is the whole of what the ioctl needs.
//
// Every failure here exits nonzero, and one case is not a failure: a
// Secret that does not exist. What separates them is what happens
// next. bluetoothd starts with whatever tree it finds, so an empty
// tree is correct for an adapter that has paired nothing, and wrong
// for an adapter whose keys this program could not read. In the second
// case the paired controllers would not connect, and the operator
// would then write the empty tree back over the stored keys, so a
// failed read would destroy the bonds it failed to read. A nonzero
// exit holds the pod in Init, leaves the Secret alone, and shows in
// kubectl. The kubelet's restart is the retry.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	// namespaceVar names the operator's own namespace, which the pod
	// spec supplies through the downward API. The Secret lives beside
	// the pod that made it, and a pod cannot read its own namespace
	// from anywhere else without asking the API server.
	namespaceVar = "POD_NAMESPACE"

	// rootVar overrides the directory the bonds are written into. The
	// default is where BlueZ reads, and the variable exists so a test
	// or a deployment that mounts the tree elsewhere states it once.
	rootVar     = "BLUETOOTH_BONDS_ROOT"
	defaultRoot = "/var/lib/bluetooth"

	// adapterTimeout bounds the wait for an adapter that reports a real
	// address. A USB dongle takes about a second from enumeration, and
	// this leaves room for a machine that enumerates its hardware
	// slowly at a cold boot.
	adapterTimeout = 30 * time.Second

	// adapterPoll is how often the wait asks the kernel again. There is
	// no event to wait on: the kernel offers no netlink notice for an
	// adapter that has finished powering on, and an HCI monitor socket
	// would be a second protocol for a wait that lasts a second.
	adapterPoll = 250 * time.Millisecond
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "bondfetch: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	namespace := os.Getenv(namespaceVar)
	if namespace == "" {
		return fmt.Errorf("%s is unset; the pod spec must supply it from metadata.namespace", namespaceVar)
	}
	root := os.Getenv(rootVar)
	if root == "" {
		root = defaultRoot
	}

	socket, err := openHCISocket()
	if err != nil {
		return err
	}
	defer socket.Close()

	adapter, err := waitForAdapter(socket.deviceInfo, adapterTimeout, adapterPoll)
	if err != nil {
		return err
	}
	fmt.Printf("bondfetch: %s carries the adapter %s\n", adapter.Name, adapter.Address)

	client, err := inClusterClient()
	if err != nil {
		return err
	}
	return materialize(client, namespace, adapter.Address, root)
}

// materialize writes one adapter's stored bonds into the tree
// bluetoothd reads. It is separate from run so that a test drives it
// against an httptest server, with no adapter and no cluster.
func materialize(api *apiClient, namespace string, adapter bonds.Address, root string) error {
	var secret bonds.Secret
	err := api.get(bonds.SecretPath(namespace, adapter), &secret)
	if errors.Is(err, errNotFound) {
		// The first start of a machine whose adapter has paired
		// nothing. bluetoothd creates the tree itself at the first
		// pairing, and the operator writes the Secret from it.
		fmt.Printf("bondfetch: no stored bonds for %s\n", adapter)
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", bonds.SecretName(adapter), err)
	}

	tree := secret.Tree()
	if err := bonds.WriteTree(root, adapter, tree); err != nil {
		return fmt.Errorf("writing the bonds under %s: %w", root, err)
	}
	fmt.Printf("bondfetch: wrote %d bonds for %s under %s\n", len(tree), adapter, root)
	return nil
}
