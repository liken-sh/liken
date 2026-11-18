package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// A paired controller has two files. The info file holds the link key,
// and the cache entry holds the SDP service records.
const (
	testInfo  = "[LinkKey]\nKey=0123456789ABCDEF0123456789ABCDEF\nType=4\n"
	testCache = "[ServiceRecords]\n0x00010000=35760900000A00010000\n"
)

var testFiles = bonds.Files{Info: []byte(testInfo), Cache: []byte(testCache)}

// testAPI returns a client for a test server that sends the token
// "test-token".
func testAPI(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	return apiclient.New(server.URL, server.Client(), credentials)
}

// storedBonds serves one `Secret` for each bond in the tree, as the
// list that the adapter's label selects. A request for any other path,
// such as one `Secret` by name, fails the test.
func storedBonds(t *testing.T, tree bonds.Tree) http.Handler {
	t.Helper()
	list := bonds.SecretList{}
	for device, files := range tree {
		list.Items = append(list.Items, *bonds.NewBondSecret("bluetooth", testAddress, device, files, nil, bonds.Owner{}))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/bluetooth/secrets" {
			t.Errorf("the request path is %q, want the Secrets of namespace bluetooth", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("labelSelector"); got != "bluetooth.liken.sh/adapter=04-4a-69-66-92-27" {
			t.Errorf("the labelSelector is %q, want the adapter label", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("the Authorization header is %q, want %q", got, "Bearer test-token")
		}
		body, err := json.Marshal(list)
		if err != nil {
			t.Fatalf("encoding the Secret list: %v", err)
		}
		_, _ = w.Write(body)
	})
}

func TestMaterializeWritesTheTreeBlueZReads(t *testing.T) {
	device, err := bonds.ParseAddress("7C:66:EF:22:E7:80")
	if err != nil {
		t.Fatalf("parsing the device address: %v", err)
	}
	api := testAPI(t, storedBonds(t, bonds.Tree{device: testFiles}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materializing the bonds: %v", err)
	}

	// Without the cache entry, a BR/EDR HID device connects and then
	// disconnects, because bluetoothd reads the HID SDP record from that
	// file and does not run service discovery again for a bonded device.
	cases := []struct {
		path string
		want string
	}{
		{path: filepath.Join("04:4A:69:66:92:27", "7C:66:EF:22:E7:80", "info"), want: testInfo},
		{path: filepath.Join("04:4A:69:66:92:27", "cache", "7C:66:EF:22:E7:80"), want: testCache},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join(root, c.path))
			if err != nil {
				t.Fatalf("reading %s: %v", c.path, err)
			}
			if string(contents) != c.want {
				t.Errorf("the file holds %q, want %q", contents, c.want)
			}
		})
	}
}

// Some `Secret`s key the info file by the bare device address, with no
// ".info" suffix. materialize restores the bond from that layout too.
func TestMaterializeReadsASecretInTheBareAddressLayout(t *testing.T) {
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"data":{"7c-66-ef-22-e7-80":"` +
			base64.StdEncoding.EncodeToString([]byte(testInfo)) + `"}}]}`))
	}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materializing the bonds: %v", err)
	}

	path := filepath.Join(root, "04:4A:69:66:92:27", "7C:66:EF:22:E7:80", "info")
	info, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(info) != testInfo {
		t.Errorf("the info file holds %q, want %q", info, testInfo)
	}
}

// An adapter that has paired nothing, as on a machine's first start,
// has no `Secret`s. materialize writes nothing and succeeds.
func TestMaterializeAcceptsAnAdapterWithNoBonds(t *testing.T) {
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materializing the bonds: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading the tree: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("the tree holds %v, want no entries because the adapter has no bonds", entries)
	}
}

// A failed read is not an adapter with no bonds. If bluetoothd started
// with an empty tree, the paired controllers would not connect, and a
// person would have to pair each one again.
func TestMaterializeFailsWhenTheSecretsCannotBeRead(t *testing.T) {
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	if err := materialize(api, "bluetooth", testAddress, t.TempDir()); err == nil {
		t.Fatal("materialize returned no error when listing the Secrets returned status 500")
	}
}

func TestMaterializeWritesEveryStoredBond(t *testing.T) {
	one, err := bonds.ParseAddress("7C:66:EF:22:E7:80")
	if err != nil {
		t.Fatalf("parsing the first device address: %v", err)
	}
	two, err := bonds.ParseAddress("A0:AB:51:33:B7:12")
	if err != nil {
		t.Fatalf("parsing the second device address: %v", err)
	}
	api := testAPI(t, storedBonds(t, bonds.Tree{one: testFiles, two: testFiles}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materializing the bonds: %v", err)
	}

	tree, err := bonds.ReadTree(root, testAddress)
	if err != nil {
		t.Fatalf("reading the tree: %v", err)
	}
	if len(tree) != 2 {
		t.Fatalf("the tree holds %d bonds, want 2: %v", len(tree), tree)
	}
}

// materialize reads the per-adapter `Secret` and the per-bond `Secret`s.
// For a device in both, it writes the per-bond copy, because the
// operator keeps that one current.
func TestMaterializeReadsBothLayoutsAndPrefersThePerBondSecret(t *testing.T) {
	device, err := bonds.ParseAddress("7C:66:EF:22:E7:80")
	if err != nil {
		t.Fatalf("parsing the device address: %v", err)
	}
	stale := bonds.Files{Info: []byte("[LinkKey]\nKey=OLD\n")}
	list := bonds.SecretList{Items: []bonds.Secret{
		*bonds.NewBondSecret("bluetooth", testAddress, device, testFiles, nil, bonds.Owner{}),
		{
			Metadata: bonds.SecretMeta{Name: bonds.SecretName(testAddress)},
			Data:     map[string][]byte{"7c-66-ef-22-e7-80.info": stale.Info},
		},
	}}
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := json.Marshal(list)
		if err != nil {
			t.Fatalf("encoding the Secret list: %v", err)
		}
		_, _ = w.Write(body)
	}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materializing the bonds: %v", err)
	}

	tree, err := bonds.ReadTree(root, testAddress)
	if err != nil {
		t.Fatalf("reading the tree: %v", err)
	}
	if got := string(tree[device].Info); got != testInfo {
		t.Errorf("the info file holds %q, want the per-bond Secret's copy", got)
	}
}
