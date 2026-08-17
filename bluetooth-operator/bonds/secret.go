package bonds

// The Secret that carries an adapter's bonds between pods.
//
// One Secret holds one adapter's whole tree: two entries per paired
// device, one for each of the files the device has. The Secret's own
// name carries the adapter's address, so the keys follow the radio and
// not the machine.
//
// The Secret lives in the operator's own namespace. Nothing outside
// the operator reads it, and a link key in it is as good as the
// controller itself to whoever can read it.
//
// Like the ResourceSlice types in this repository, these structs carry
// only the fields both programs read or write. The API's Secret also
// has stringData, immutable, and every field an ordinary object has,
// and none of them changes what a bond needs.

import "strings"

const (
	// The Secret's name is this prefix and the adapter's address, so a
	// person listing Secrets reads which radio each one belongs to.
	secretPrefix = "bluetooth-bonds-"

	// SecretType is Opaque, which is what a Secret with data of no
	// standard shape carries. The typed values, such as
	// kubernetes.io/tls, each fix a set of keys, and these keys are
	// device addresses.
	SecretType = "Opaque"

	// The labels on every Secret this operator writes. The name label
	// is the standard one that says which application owns the object.
	// The adapter label repeats the address that the Secret's name
	// carries, because a label is selectable and a name is not, so a
	// person can list every Secret for one radio.
	nameLabel    = "app.kubernetes.io/name"
	operatorName = "bluetooth-operator"
	adapterLabel = "bluetooth.liken.sh/adapter"

	// The suffixes that separate a device's two files inside one
	// Secret. A Secret key accepts a letter, a digit, and any of - _
	// and . , so the address and a dotted suffix both fit, and a
	// person reading kubectl describe reads which device each file
	// belongs to. The alternative shapes both cost more: a Secret for
	// each device multiplies the objects the init container reads,
	// and one key holding an archive of both files makes every read
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

// Secret is one adapter's stored bonds as the API server holds them.
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

// SecretMeta carries the identity and the resourceVersion. The version
// travels on a write, so a second writer gets a conflict instead of
// losing the first writer's bonds.
type SecretMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
}

// SecretName is the name of the Secret that holds one adapter's bonds.
func SecretName(adapter Address) string {
	return secretPrefix + adapter.Key()
}

// SecretLabels are the labels every one of these Secrets carries.
func SecretLabels(adapter Address) map[string]string {
	return map[string]string{
		nameLabel:    operatorName,
		adapterLabel: adapter.Key(),
	}
}

// SecretPath is the API server's URL for one adapter's Secret. Both
// programs that reach the API ask for the same path, so the path is
// derived here rather than written twice.
func SecretPath(namespace string, adapter Address) string {
	return "/api/v1/namespaces/" + namespace + "/secrets/" + SecretName(adapter)
}

// NewSecret builds the object the operator writes for one adapter.
func NewSecret(namespace string, adapter Address, tree Tree) *Secret {
	data := make(map[string][]byte, 2*len(tree))
	for device, stored := range tree {
		data[secretKey(device, infoSuffix)] = stored.Info
		// A device with no cache entry gets no cache key, so that the
		// restore writes no cache file for it. An empty value would
		// restore an empty file, and that is a different fact.
		if len(stored.Cache) > 0 {
			data[secretKey(device, cacheSuffix)] = stored.Cache
		}
	}
	return &Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata: SecretMeta{
			Name:      SecretName(adapter),
			Namespace: namespace,
			Labels:    SecretLabels(adapter),
		},
		Type: SecretType,
		Data: data,
	}
}

// Tree reads the bonds back out of a stored Secret.
//
// A key that does not name a device's file is skipped. The alternative is to
// fail the whole read, and that would start bluetoothd with no bonds
// at all over one bad key, which disconnects every controller that the
// other keys would have connected.
//
// The three passes run in this order for two reasons. Some Secrets in
// the field key a device's info file by the bare address, with no
// suffix, while the current layout suffixes it with .info. The second
// pass reads the bare-address form only where the first pass found
// nothing, so the suffixed keys win while a Secret carries both. A
// Secret carries both only until the operator next rewrites it,
// because the operator replaces the whole object on the first pass
// that sees a difference. The cache pass runs last because it attaches
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
