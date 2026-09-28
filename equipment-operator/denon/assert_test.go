// The assertion helpers the denon test files share. Each one fails the
// test with the value it read against the value it wanted, so a failure
// reads without a debugger.

package denon

import (
	"reflect"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// The bound on every wait in these tests: long enough that a loaded
// machine still passes, short enough that a broken program fails in
// seconds.
const testTimeout = 2 * time.Second

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wanted no error, got %v", err)
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// mustMatchState compares two states, which carry a map and so cannot
// satisfy comparable.
func mustMatchState(t *testing.T, got, want equipment.State) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
