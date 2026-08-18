package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// The two files one paired controller has: the link key in its
// info file, and the SDP records in its cache entry.
const (
	testInfo  = "[LinkKey]\nKey=0123456789ABCDEF0123456789ABCDEF\nType=4\n"
	testCache = "[ServiceRecords]\n0x00010000=35760900000A00010000\n"
)

// testFiles is one device's stored files, the shape a restore writes.
var testFiles = bonds.Files{Info: []byte(testInfo), Cache: []byte(testCache)}

// testAPI points a client at a test server, with a credentials
// directory the test owns.
func testAPI(t *testing.T, handler http.Handler) *apiClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return newAPIClient(server.URL, server.Client(), credentials)
}

// storedBonds serves one Secret for each bond, out of the collection
// and under the adapter's label. Any other path fails, so a request
// that named one Secret rather than the label shows up here.
func storedBonds(t *testing.T, tree bonds.Tree) http.Handler {
	t.Helper()
	list := bonds.SecretList{}
	for device, files := range tree {
		list.Items = append(list.Items, *bonds.NewBondSecret("bluetooth", testAddress, device, files, bonds.Owner{}))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/bluetooth/secrets" {
			t.Errorf("path = %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("labelSelector"); got != "bluetooth.liken.sh/adapter=04-4a-69-66-92-27" {
			t.Errorf("labelSelector = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		body, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	})
}

func TestMaterializeWritesTheTreeBlueZReads(t *testing.T) {
	device, err := bonds.ParseAddress("7C:66:EF:22:E7:80")
	if err != nil {
		t.Fatal(err)
	}
	api := testAPI(t, storedBonds(t, bonds.Tree{device: testFiles}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	// Both files, at the paths bluetoothd reads them from. Without the
	// cache entry a BR/EDR HID device connects and drops again: the
	// input profile parses the HID SDP record out of that file, and
	// bluetoothd runs no new discovery for a device it holds a bond
	// with.
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
				t.Errorf("contents = %q, want %q", contents, c.want)
			}
		})
	}
}

// Some Secrets in the field key a device by the bare address, with no
// suffix, holding the info file. The pod reads that layout and
// restores the bond, rather than exiting on keys it does not
// recognise.
func TestMaterializeReadsASecretInTheBareAddressLayout(t *testing.T) {
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"data":{"7c-66-ef-22-e7-80":"` +
			base64.StdEncoding.EncodeToString([]byte(testInfo)) + `"}}]}`))
	}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	path := filepath.Join(root, "04:4A:69:66:92:27", "7C:66:EF:22:E7:80", "info")
	info, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(info) != testInfo {
		t.Errorf("info = %q", info)
	}
}

// An adapter that has paired nothing has no Secrets. That is the
// ordinary first start of a machine, so it writes nothing and exits
// zero, and bluetoothd starts on an empty tree.
func TestMaterializeAcceptsAnAdapterWithNoBonds(t *testing.T) {
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("it wrote %v into a tree it has no bonds for", entries)
	}
}

// A read that fails is not an adapter that has paired nothing.
// bluetoothd must not start on an empty tree here: the controllers
// would not connect, and a person would pair each one again.
func TestMaterializeFailsWhenTheSecretsCannotBeRead(t *testing.T) {
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	if err := materialize(api, "bluetooth", testAddress, t.TempDir()); err == nil {
		t.Fatal("materialize reported success for a Secret it could not read")
	}
}

func TestMaterializeWritesEveryStoredBond(t *testing.T) {
	one, err := bonds.ParseAddress("7C:66:EF:22:E7:80")
	if err != nil {
		t.Fatal(err)
	}
	two, err := bonds.ParseAddress("A0:AB:51:33:B7:12")
	if err != nil {
		t.Fatal(err)
	}
	api := testAPI(t, storedBonds(t, bonds.Tree{one: testFiles, two: testFiles}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	tree, err := bonds.ReadTree(root, testAddress)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 {
		t.Fatalf("got %d bonds, want 2: %v", len(tree), tree)
	}
}

// The older per-adapter Secret has the same label, so a machine
// that has paired nothing since that layout changed still restores its
// bonds. A device in both layouts takes the per-bond copy, which is
// the one the operator keeps current.
func TestMaterializeReadsBothLayoutsAndPrefersThePerBondSecret(t *testing.T) {
	device, err := bonds.ParseAddress("7C:66:EF:22:E7:80")
	if err != nil {
		t.Fatal(err)
	}
	stale := bonds.Files{Info: []byte("[LinkKey]\nKey=OLD\n")}
	list := bonds.SecretList{Items: []bonds.Secret{
		*bonds.NewBondSecret("bluetooth", testAddress, device, testFiles, bonds.Owner{}),
		{
			Metadata: bonds.SecretMeta{Name: bonds.SecretName(testAddress)},
			Data:     map[string][]byte{"7c-66-ef-22-e7-80.info": stale.Info},
		},
	}}
	api := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}))
	root := t.TempDir()

	if err := materialize(api, "bluetooth", testAddress, root); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	tree, err := bonds.ReadTree(root, testAddress)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(tree[device].Info); got != testInfo {
		t.Errorf("info = %q, want the per-bond Secret's copy", got)
	}
}
