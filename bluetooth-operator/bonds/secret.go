package bonds

// The Secret that carries an adapter's bonds between pods.
//
// One Secret holds one adapter's whole tree: one entry per paired
// device, keyed by the device's address, holding that device's info
// file. The Secret's own name carries the adapter's address, so the
// keys follow the radio and not the machine.
//
// The Secret lives in the operator's own namespace. Nothing outside
// the operator reads it, and a link key in it is as good as the
// controller itself to whoever can read it.
//
// Like the ResourceSlice types in this repository, these structs carry
// only the fields both programs read or write. The API's Secret also
// has stringData, immutable, and every field an ordinary object has,
// and none of them changes what a bond needs.

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
)

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
	data := make(map[string][]byte, len(tree))
	for device, info := range tree {
		data[device.Key()] = info
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
// A key that is not an address names no device, so it is skipped. The
// alternative is to fail the whole read, and that would start
// bluetoothd with no bonds at all over one bad key, which disconnects
// every controller that the other keys would have connected.
func (s *Secret) Tree() Tree {
	tree := make(Tree, len(s.Data))
	for key, info := range s.Data {
		device, err := ParseAddress(key)
		if err != nil {
			continue
		}
		tree[device] = info
	}
	return tree
}
