// bondfetch is the init container of the pod that runs bluetoothd. It
// reads the bonds of the pod's adapter from Kubernetes `Secret`s and
// writes them into the directory tree that bluetoothd reads, an
// `emptyDir` that the bluetoothd container mounts. It also restores the
// adapter's identity key and writes the privacy and btmon settings of
// the `Adapter` (privacy.go and settings.go), because bluetoothd must
// find all of these in place when it starts.
//
// The operator stores each bond in its own `Secret`, labelled with the
// adapter (see the `bonds` package), and writes the `Secret` again when
// bluetoothd changes the tree. bluetoothd is not running yet, so this
// program has no list of paired devices and selects the `Secret`s by
// the adapter label alone. A per-adapter `Secret` that holds every bond
// of the adapter has the same label, so this program reads it too.
//
// The adapter's address comes from the kernel through an HCI socket
// ioctl. The ioctl works because the pod runs in the host network
// namespace.
//
// The program exits nonzero on every failure, and an adapter with no
// `Secret`s is not a failure. bluetoothd starts with whatever tree it
// finds, so a bluetoothd that started after a failed read would leave
// the paired controllers unable to connect. A nonzero exit keeps the pod
// in Init, where `kubectl` shows it, leaves the `Secret`s unchanged, and
// the kubelet restarts the container.
package main

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	// namespaceVar holds the pod's namespace, which the pod spec sets
	// through the downward API. The `Secret`s are in that namespace.
	namespaceVar = "POD_NAMESPACE"

	// rootVar overrides the directory of the bond tree, for a test or a
	// deployment that mounts it somewhere other than BlueZ's default.
	rootVar     = "BLUETOOTH_BONDS_ROOT"
	defaultRoot = "/var/lib/bluetooth"

	// adapterTimeout is how long the program waits for the kernel to
	// report an adapter with a non-zero address, which takes longest on
	// a machine that enumerates its hardware slowly at a cold boot.
	adapterTimeout = 30 * time.Second

	// The wait polls because the kernel sends no netlink notification
	// when an adapter finishes powering on, and an HCI monitor socket
	// would add a second protocol for a short wait.
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
		return fmt.Errorf("%s is unset, and the pod spec must set it from metadata.namespace", namespaceVar)
	}
	root := os.Getenv(rootVar)
	if root == "" {
		root = defaultRoot
	}
	settings := os.Getenv(settingsVar)
	if settings == "" {
		settings = defaultSettings
	}

	socket, err := openHCISocket()
	if err != nil {
		return err
	}
	defer socket.Close()

	adapter, err := waitForAdapter(socket.deviceInfo)
	if err != nil {
		return err
	}
	fmt.Printf("bondfetch: found adapter %s at address %s\n", adapter.Name, adapter.Address)

	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		return err
	}
	if err := materialize(client, namespace, adapter.Address, root); err != nil {
		return err
	}
	privacy, err := writeSettings(client, adapter.Address, settings)
	if err != nil {
		return err
	}
	return restoreIdentity(client, namespace, adapter.Address, root, privacy)
}

// materialize writes the stored bonds of one adapter into the tree that
// bluetoothd reads.
//
// It merges the per-adapter `Secret` first and the per-bond `Secret`s
// after it, so when a device is in both, the per-bond copy wins. The
// operator keeps the per-bond copy current and does not delete the
// per-adapter `Secret`, so both can exist.
func materialize(api *apiclient.Client, namespace string, adapter bonds.Address, root string) error {
	path := bonds.SecretsPath(namespace) + "?labelSelector=" + url.QueryEscape(bonds.AdapterSelector(adapter))
	list, err := apiclient.Get[bonds.SecretList](api, path)
	if err != nil {
		return fmt.Errorf("listing the bonds for %s: %w", adapter, err)
	}

	tree := bonds.Tree{}
	for _, secret := range list.Items {
		if !secret.OneBond() {
			tree.Merge(secret.Tree())
		}
	}
	for _, secret := range list.Items {
		if secret.OneBond() {
			tree.Merge(secret.Tree())
		}
	}
	if len(tree) == 0 {
		// bluetoothd creates the tree at the first pairing, and the
		// operator then writes a `Secret` from it.
		fmt.Printf("bondfetch: no stored bonds for adapter %s\n", adapter)
		return nil
	}

	if err := bonds.WriteTree(root, adapter, tree); err != nil {
		return fmt.Errorf("writing the bonds under %s: %w", root, err)
	}
	fmt.Printf("bondfetch: wrote %d bonds for adapter %s under %s, from %d Secrets\n",
		len(tree), adapter, root, len(list.Items))
	return nil
}
