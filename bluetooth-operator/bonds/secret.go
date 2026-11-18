package bonds

// Each paired device's bond is stored in its own Secret: the two files
// BlueZ keeps for the device, and the evdev capability snapshot that
// the input relay creates the device's virtual input nodes from.
//
// The Secret is named after the device's address, carries a label with
// the adapter's address, and is owned by the device's Peripheral. The
// name and label tie the keys to the radio and not to the machine, and
// the owner reference makes the garbage collector delete the Secret
// when the Peripheral is deleted.
//
// An older layout keeps all of an adapter's bonds in one Secret named
// for the adapter. The operator does not write that layout, but the
// reader accepts it so that a bond stored only there still restores.
// Both layouts use the same keys inside the Secret.
//
// The Secret is in the operator's namespace. Anyone who can read a
// link key in it can impersonate the controller, so nothing else
// should have access to it.
//
// These structs declare only the Secret fields that the operator and
// the init container use.

import "strings"

const (
	// BondSecretPrefix followed by the device's address is the name of
	// the Secret for one bond, so kubectl get secrets shows which
	// controller each one belongs to.
	BondSecretPrefix = "bluetooth-bond-"

	// secretPrefix is the name prefix of the older per-adapter Secret.
	// The operator logs a message when one still exists, so that a
	// person deletes it.
	secretPrefix = "bluetooth-bonds-"

	// SecretType is Opaque because the other Secret types, such as
	// kubernetes.io/tls, require a fixed set of keys and these keys are
	// device addresses.
	SecretType = "Opaque"

	// The labels on every Secret this operator writes. AdapterLabel is
	// the address of the radio that the bond belongs to. The operator and
	// the init container list one radio's Secrets by this label, because
	// a Secret's name has the device address and not the adapter address.
	nameLabel    = "app.kubernetes.io/name"
	operatorName = "bluetooth-operator"
	AdapterLabel = "bluetooth.liken.sh/adapter"

	// The key of each of a device's BlueZ files is the device's address
	// plus one of these suffixes. The address stays in the key because
	// the older per-adapter layout stores several devices in one Secret,
	// and one reader handles both layouts. Only secretKey and deviceOf
	// build and split these keys, so the operator's writes and the init
	// container's reads agree.
	infoSuffix  = ".info"
	cacheSuffix = ".cache"

	// evdevSuffix ends the key of a device's evdev capability snapshot.
	// The snapshot is not a BlueZ file, so the init container does not
	// write it into bluetoothd's tree, and the operator reads it from the
	// API. It is in the bond's Secret so that deleting the bond deletes
	// it too.
	evdevSuffix = ".evdev"
)

// secretKey returns the key of one of a device's files in the Secret.
func secretKey(device Address, suffix string) string {
	return device.Key() + suffix
}

// deviceOf returns the device whose address is key without suffix. It
// returns false when key does not end in suffix or the rest is not an
// address.
func deviceOf(key, suffix string) (Address, bool) {
	name, found := strings.CutSuffix(key, suffix)
	if !found {
		return Address{}, false
	}
	device, err := ParseAddress(name)
	if err != nil {
		return Address{}, false
	}
	return device, true
}

// Secret is a Secret object of the API server. It holds one device's
// bond in the current layout, or one adapter's bonds in the older one.
//
// Data is map[string][]byte because the API encodes each value of a
// Secret's data field as base64, and encoding/json encodes and decodes
// []byte the same way.
type Secret struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   SecretMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string][]byte `json:"data,omitempty"`
}

// SecretMeta holds the name, namespace, labels, resourceVersion, and
// owner of a Secret. Every update sends the resourceVersion, so when
// two writers race, the second gets a conflict and does not overwrite
// the first writer's bonds. The owner is the bond's Peripheral.
type SecretMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	OwnerReferences []Owner           `json:"ownerReferences,omitempty"`
}

// Owner is an owner reference. The garbage collector deletes the
// Secret when its owner is deleted. The UID identifies one instance of
// the owner, so a Peripheral that is deleted and created again does
// not inherit the old one's Secret.
type Owner struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

// SecretList is the API server's response to a list of Secrets.
type SecretList struct {
	Items []Secret `json:"items"`
}

// OneBond reports whether the Secret has the current layout, one
// device's bond. The init container uses it to merge the older
// per-adapter Secrets first, so that the per-bond Secret wins when a
// device is in both.
func (s *Secret) OneBond() bool {
	return strings.HasPrefix(s.Metadata.Name, BondSecretPrefix)
}

// SecretName is the name of the older Secret that holds all of one
// adapter's bonds.
func SecretName(adapter Address) string {
	return secretPrefix + adapter.Key()
}

// BondSecretName is the name of the Secret for one device's bond.
func BondSecretName(device Address) string {
	return BondSecretPrefix + device.Key()
}

// SecretLabels returns the labels of every bond Secret of adapter.
func SecretLabels(adapter Address) map[string]string {
	return map[string]string{
		nameLabel:    operatorName,
		AdapterLabel: adapter.Key(),
	}
}

// SecretPath is the API path of the older per-adapter Secret.
func SecretPath(namespace string, adapter Address) string {
	return "/api/v1/namespaces/" + namespace + "/secrets/" + SecretName(adapter)
}

// BondSecretPath is the API path of one device's bond Secret.
func BondSecretPath(namespace string, device Address) string {
	return "/api/v1/namespaces/" + namespace + "/secrets/" + BondSecretName(device)
}

// SecretsPath is the API path of the namespace's Secrets collection.
func SecretsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets"
}

// AdapterSelector is the label selector for one radio's Secrets. The
// init container has no list of paired devices, so it lists the bonds
// by the adapter it claimed.
func AdapterSelector(adapter Address) string {
	return AdapterLabel + "=" + adapter.Key()
}

// Snapshots returns the stored evdev capability snapshot of each
// device that has one. The operator creates a controller's virtual
// input devices from the snapshot before the controller connects, so a
// claim can be prepared for a controller that is asleep.
func (s *Secret) Snapshots() map[Address][]byte {
	snapshots := map[Address][]byte{}
	for key, snapshot := range s.Data {
		device, named := deviceOf(key, evdevSuffix)
		if !named || len(snapshot) == 0 {
			continue
		}
		snapshots[device] = snapshot
	}
	return snapshots
}

// Snapshot returns one device's stored snapshot, or nil for a device
// that has never connected.
func (s *Secret) Snapshot(device Address) []byte {
	return s.Data[secretKey(device, evdevSuffix)]
}

// NewBondSecret builds the Secret that the operator writes for one
// bond. snapshot is empty until the controller has connected once.
func NewBondSecret(namespace string, adapter, device Address, stored Files, snapshot []byte, owner Owner) *Secret {
	data := map[string][]byte{secretKey(device, infoSuffix): stored.Info}
	// An empty cache value would restore an empty cache file, so a
	// device with no cache file gets no cache key.
	if len(stored.Cache) > 0 {
		data[secretKey(device, cacheSuffix)] = stored.Cache
	}
	if len(snapshot) > 0 {
		data[secretKey(device, evdevSuffix)] = snapshot
	}
	secret := &Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata: SecretMeta{
			Name:      BondSecretName(device),
			Namespace: namespace,
			Labels:    SecretLabels(adapter),
		},
		Type: SecretType,
		Data: data,
	}
	if owner.UID != "" {
		secret.Metadata.OwnerReferences = []Owner{owner}
	}
	return secret
}

// Tree returns the bonds that the Secret stores. It skips a key that is
// not a device's file, because failing the whole read would start
// bluetoothd with no bonds at all.
//
// Some existing Secrets store a device's info file under its bare
// address, with no .info suffix. Tree reads the suffixed keys first and
// then the bare ones, so the suffixed key wins when a Secret has both.
// It attaches cache files last, only to devices that have an info file.
func (s *Secret) Tree() Tree {
	tree := make(Tree, len(s.Data))
	for key, info := range s.Data {
		device, named := deviceOf(key, infoSuffix)
		if !named || len(info) == 0 {
			continue
		}
		tree[device] = Files{Info: info}
	}
	for key, info := range s.Data {
		device, err := ParseAddress(key)
		if err != nil || len(info) == 0 {
			continue
		}
		if _, found := tree[device]; found {
			continue
		}
		tree[device] = Files{Info: info}
	}
	for key, cache := range s.Data {
		device, named := deviceOf(key, cacheSuffix)
		if !named {
			continue
		}
		stored, paired := tree[device]
		// A cache file without an info file is not a bond. Restoring it
		// would write a cache entry for a device that this adapter is not
		// paired with.
		if !paired {
			continue
		}
		stored.Cache = cache
		tree[device] = stored
	}
	return tree
}
