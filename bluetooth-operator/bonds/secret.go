package bonds

// The Secret that carries a bond between pods.
//
// One Secret holds one bond: the two files that one paired device has.
// Its name states the device's address, it has a label naming the
// adapter the bond belongs to, and it lists that device's Pairing as
// its owner. So the keys follow the radio and not the machine, and
// deleting the Pairing collects the Secret through ordinary garbage
// collection.
//
// An older layout put every one of an adapter's bonds in one Secret
// named for the adapter. Nothing writes that layout now, and the
// reader still accepts it, because a machine that has not paired
// anything since the change still holds its keys there. Both layouts
// hold the same keys inside, so one reader handles both.
//
// The Secret is in the operator's own namespace. Nothing outside
// the operator reads it, and a link key in it is as good as the
// controller itself to whoever can read it.
//
// Like the ResourceSlice types in this repository, these structs hold
// only the fields both programs read or write. The API's Secret also
// has stringData, immutable, and every field an ordinary object has,
// and none of them changes what a bond needs.

import "strings"

const (
	// BondSecretPrefix and the device's address name the Secret that
	// holds one bond, so a person listing Secrets reads which controller
	// each one belongs to.
	BondSecretPrefix = "bluetooth-bond-"

	// secretPrefix names the older per-adapter Secret. Nothing writes
	// one now. A reader still recognizes the name, because the operator
	// reports the leftover object and a person deletes it.
	secretPrefix = "bluetooth-bonds-"

	// SecretType is Opaque, which is the type of a Secret with data of
	// no standard shape. The typed values, such as
	// kubernetes.io/tls, each fix a set of keys, and these keys are
	// device addresses.
	SecretType = "Opaque"

	// The labels on every Secret this operator writes. The name label
	// is the standard one that says which application owns the object.
	// The adapter label names the radio the bond belongs to, because a
	// label is selectable and a name is not: the init container lists
	// one radio's Secrets by it, and the Secret's own name states the
	// device instead.
	nameLabel    = "app.kubernetes.io/name"
	operatorName = "bluetooth-operator"
	AdapterLabel = "bluetooth.liken.sh/adapter"

	// The suffixes that separate a device's two files inside its
	// Secret. A Secret key accepts a letter, a digit, and any of - _
	// and . , so the address and a dotted suffix both fit, and a
	// person reading kubectl describe reads which device each file
	// belongs to. The address stays in the key because the older
	// per-adapter layout put several devices in one object, and one
	// reader handles both. The alternative to the two suffixes is one
	// key holding an archive of both files, and that makes every read
	// parse a format this package would have to define.
	//
	// Nothing outside this file writes these strings. secretKey and
	// deviceOf are the only two places that join and split them, so
	// the operator's write and the init container's read cannot spell
	// the layout differently.
	infoSuffix  = ".info"
	cacheSuffix = ".cache"
)

// secretKey names one of a device's files inside the Secret.
func secretKey(device Address, suffix string) string {
	return device.Key() + suffix
}

// deviceOf answers with the device a key names, when the key names a
// device's file of that kind.
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

// Secret is the stored bonds as the API server holds them: one
// device's bond in the current layout, and one adapter's whole tree in
// the older one.
//
// Data is map[string][]byte because that is the shape the API's data
// field takes on the wire: encoding/json writes a []byte as base64 and
// reads it back, which is exactly what a Secret value is. An info file
// is text today, and nothing here depends on that.
type Secret struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   SecretMeta        `json:"metadata"`
	Type       string            `json:"type,omitempty"`
	Data       map[string][]byte `json:"data,omitempty"`
}

// SecretMeta holds the identity, the resourceVersion, and the owner.
// The version goes with every write, so a second writer gets a
// conflict instead of losing the first writer's bonds. The owner is
// the bond's Pairing, so deleting the Pairing collects the keys with
// it.
type SecretMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	OwnerReferences []Owner           `json:"ownerReferences,omitempty"`
}

// Owner ties a Secret's lifetime to the object that owns it. The UID
// matters: a reference names one instance, so a Pairing that is
// deleted and created again does not inherit the old one's Secret.
type Owner struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

// SecretList is a list of Secrets as the API server answers with them.
type SecretList struct {
	Items []Secret `json:"items"`
}

// OneBond reports whether this Secret holds a single device's bond.
// The init container reads both layouts and applies the older
// per-adapter Secret first, so a bond that is in both takes the value
// the operator keeps current.
func (s *Secret) OneBond() bool {
	return strings.HasPrefix(s.Metadata.Name, BondSecretPrefix)
}

// SecretName is the name of the older Secret that holds one adapter's
// whole tree.
func SecretName(adapter Address) string {
	return secretPrefix + adapter.Key()
}

// BondSecretName is the name of the Secret that holds one device's
// bond.
func BondSecretName(device Address) string {
	return BondSecretPrefix + device.Key()
}

// SecretLabels are the labels every one of these Secrets has.
func SecretLabels(adapter Address) map[string]string {
	return map[string]string{
		nameLabel:    operatorName,
		AdapterLabel: adapter.Key(),
	}
}

// SecretPath is the API server's URL for the older per-adapter Secret.
func SecretPath(namespace string, adapter Address) string {
	return "/api/v1/namespaces/" + namespace + "/secrets/" + SecretName(adapter)
}

// BondSecretPath is the API server's URL for one bond's Secret. Both
// programs that reach the API ask for the same path, so the path is
// derived here rather than written twice.
func BondSecretPath(namespace string, device Address) string {
	return "/api/v1/namespaces/" + namespace + "/secrets/" + BondSecretName(device)
}

// SecretsPath is the collection a create posts to, and the list the
// init container reads. A create names the collection, which is the
// API's rule for every resource, where every other call here names the
// object.
func SecretsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/secrets"
}

// AdapterSelector lists one radio's Secrets. The init container has no
// list of paired devices to build names from, so the label selector
// gathers the bonds for the radio this pod claimed.
func AdapterSelector(adapter Address) string {
	return AdapterLabel + "=" + adapter.Key()
}

// NewBondSecret builds the object the operator writes for one bond.
func NewBondSecret(namespace string, adapter, device Address, stored Files, owner Owner) *Secret {
	data := map[string][]byte{secretKey(device, infoSuffix): stored.Info}
	// A device with no cache entry gets no cache key, so that the
	// restore writes no cache file for it. An empty value would restore
	// an empty file, and that is a different fact.
	if len(stored.Cache) > 0 {
		data[secretKey(device, cacheSuffix)] = stored.Cache
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

// Tree reads the bonds back out of a stored Secret.
//
// A key that does not name a device's file is skipped. The alternative
// is to fail the whole read, and that would start bluetoothd with no
// bonds at all over one bad key, which disconnects every controller that the
// other keys would have connected.
//
// The three passes run in this order for two reasons. Some Secrets in
// the field key a device's info file by the bare address, with no
// suffix, while the current layout suffixes it with .info. The second
// pass reads the bare-address form only where the first pass found
// nothing, so the suffixed keys win while a Secret holds both. A
// Secret holds both only until the operator next rewrites it, because
// the operator replaces the whole object on the first pass that finds
// a difference. The cache pass runs last because it attaches
// to a device that one of the first two passes established, and a
// cache key alone establishes nothing.
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
		// A cache key with no info key beside it names a device this
		// adapter holds no bond with. Restoring it would write a cache
		// entry for hardware the machine has no claim on, which is
		// what the whole tree is filtered to keep out.
		if !paired {
			continue
		}
		stored.Cache = cache
		tree[device] = stored
	}
	return tree
}
