// The read back after a setter: it never waits on a poll, and it never
// undoes a change that landed while it read.

package wiim

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/equipment"
)

// A setter sent while a poll reads does not wait for the poll, because
// the controller's pass sends it. The poll reads the setter's family
// instead.
func TestAReadBackDuringAPollIsLeftToThePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		amp.answer(ledCommand, "0")
		led := false

		client.reads.Lock()
		mustSucceed(t, client.ApplySettings(Settings{Device: DeviceSettings{LED: &led}}))
		mustMatch(t, amp.sent(), []string{"LED_SWITCH_SET:0"})
		client.reads.Unlock()

		amp.clear()
		client.poll(context.Background())
		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand, ledCommand, buttonsCommand, cecCommand})
		mustMatch(t, *client.Settings().Device.LED, false)
	})
}

// A volume press that lands while a read back reads stays: the read
// back writes only the fields it read.
func TestAReadBackKeepsAVolumeSentDuringIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		pressed := false
		amp.mutex.Lock()
		amp.onRead = func(command string) {
			if command == ledCommand && !pressed {
				pressed = true
				mustSucceed(t, client.SetVolume(equipment.MainZone, 42))
			}
		}
		amp.mutex.Unlock()
		led := true

		mustSucceed(t, client.ApplySettings(Settings{Device: DeviceSettings{LED: &led}}))

		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 42)
	})
}
