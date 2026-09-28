package cectest_test

// A scripted streaming player takes the input for itself after another
// device's Active Source, the way one did in the first drill when the
// room woke. A test of a wake shows with it what the wake does when
// another source takes the input.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// claimsHeard reads what a follower receives for a while, and answers
// the Active Source messages in the order they arrived.
func claimsHeard(t *testing.T, follower *cec.Device, within time.Duration) []string {
	t.Helper()
	var claims []string
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		message, err := follower.Receive(10 * time.Millisecond)
		if err != nil {
			continue
		}
		if opcode, _ := message.Opcode(); opcode == cec.OpActiveSource {
			claims = append(claims, message.String())
		}
	}
	return claims
}

func TestAGrabbingPeerTakesTheInputBack(t *testing.T) {
	cases := []struct {
		name   string
		grabs  int
		sends  int
		claims []string
	}{
		{"a player that grabs once", 1, 2, []string{"4->f 82 13 00", "8->f 82 15 00", "4->f 82 13 00"}},
		{"a player that never grabs", 0, 1, []string{"4->f 82 13 00"}},
		{"a player that grabs twice", 2, 2, []string{"4->f 82 13 00", "8->f 82 15 00", "4->f 82 13 00", "8->f 82 15 00"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := cectest.NewBus()
			bus.Add(cectest.Peer{Logical: 8, Physical: 0x1500, PrimaryType: 4, Grabs: c.grabs, GrabAfter: time.Millisecond})
			_, sender := playing(t, bus, 0x1300, "node-1")
			_, listener := playing(t, bus, 0x1400, "node-2")

			for range c.sends {
				_, err := sender.Transmit(cec.ActiveSource(4, 0x1300), 0, 0)
				mustSucceed(t, err)
				time.Sleep(20 * time.Millisecond)
			}

			claims := claimsHeard(t, listener, 50*time.Millisecond)
			if len(claims) != len(c.claims) {
				t.Fatalf("heard %v, want %v", claims, c.claims)
			}
			for index := range claims {
				if claims[index] != c.claims[index] {
					t.Errorf("heard %v, want %v", claims, c.claims)
				}
			}
		})
	}
}
