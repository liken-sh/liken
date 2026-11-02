package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// trusted_root.json is the bytes that Sigstore's TUF repository
// served, which latest.sh --bump writes, and pin.toml holds their
// digest for the release's SBOM. A change to the file that no bump
// wrote, such as a newline that an editor adds, fails here, and so
// does a pin.toml that names another file or another version.
func TestTrustedRootMatchesItsPin(t *testing.T) {
	var pins map[string]map[string]string
	if _, err := toml.DecodeFile("pin.toml", &pins); err != nil {
		t.Fatal(err)
	}
	version, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	pin := pins["sigstore-trusted-root"]
	sum := sha256.Sum256(trustedRootJSON)
	if got := hex.EncodeToString(sum[:]); got != pin["sha256"] {
		t.Errorf("trusted_root.json has the sha256 %s, and pin.toml pins %q", got, pin["sha256"])
	}
	if got, want := pin["version"], strings.TrimSpace(string(version)); got != want {
		t.Errorf("pin.toml names version %q, and VERSION pins %s", got, want)
	}
}

func TestTheEmbeddedTrustedRootParses(t *testing.T) {
	if _, err := verifier(); err != nil {
		t.Fatal(err)
	}
}
