package wiim

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// A setter sent while a poll holds c.reads returns without waiting, and
// the poll reads the setter's family.
func TestThePollReadsTheFamilyOfASetterSentDuringIt(t *testing.T) {
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

// A volume change that arrives during a read back is kept, because the
// read back copies only the fields it read.
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
