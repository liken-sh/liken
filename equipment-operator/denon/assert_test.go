package denon

import (
	"reflect"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// testTimeout limits every wait in these tests. The waits run on the
// fake clock of a synctest bubble, so a long limit costs no real time.
const testTimeout = 2 * time.Second

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the call returned error %v, want none", err)
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("the value is %+v, want %+v", got, want)
	}
}

// mustMatchState uses reflect.DeepEqual because equipment.State
// contains a map and is not comparable.
func mustMatchState(t *testing.T, got, want equipment.State) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the value is %+v, want %+v", got, want)
	}
}
