// Tests of how the client recovers from failed requests. A full read is
// a poll that reads every setting family (readsEveryFamily in
// client.go). A miss is one failed poll, and an outage is pollFailures
// failed polls in a row, after which the client marks the amp
// unreachable.

package wiim

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// newTrack makes the fake's player report a different track title.
func newTrack(amp *fakeAmp) {
	amp.answer(playerCommand, strings.Replace(playerJSON, `"Title":"4C616E7465726E20536F6E67"`, `"Title":"4D6F6E6579"`, 1))
}

func breakCommand(amp *fakeAmp, command string, broken bool) {
	amp.mutex.Lock()
	defer amp.mutex.Unlock()
	amp.broken = map[string]bool{command: broken}
}

// When the request for the track's metadata fails, the next poll
// requests it again. Once a request succeeds, later polls do not.
func TestAFailedTrackReadIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		newTrack(amp)
		breakCommand(amp, metaCommand, true)
		client.poll(context.Background())

		breakCommand(amp, metaCommand, false)
		amp.answer(metaCommand, strings.Replace(metaJSON, `"title":"Lantern Song"`, `"title":"Second Song"`, 1))
		amp.clear()
		client.poll(context.Background())
		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand, metaCommand})
		mustMatch(t, client.Status().NowPlaying.Title, "Second Song")

		amp.clear()
		client.poll(context.Background())
		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
	})
}

// A track read that keeps failing is retried for trackRetries polls and
// then stops.
func TestATrackReadThatKeepsFailingStopsAfterTrackRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		newTrack(amp)
		breakCommand(amp, metaCommand, true)
		for range trackRetries + 2 {
			client.poll(context.Background())
		}

		mustMatch(t, countOf(amp.sent(), metaCommand), trackRetries)
	})
}

// Any response to the track read ends the retries, even one with a
// title other than the one the player reports. Line-in, optical, and
// internet radio sources respond that way, and without this rule the
// client would request the track on every poll.
func TestATrackReadThatRespondsIsNotRetried(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{"no track", "Failed"},
		{"another title", strings.Replace(metaJSON, `"title":"Lantern Song"`, `"title":"unknow"`, 1)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				amp, client := quietAmp(t)
				newTrack(amp)
				amp.answer(metaCommand, one.answer)
				client.poll(context.Background())

				amp.clear()
				client.poll(context.Background())
				mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
			})
		})
	}
}

// When a command that responded before fails during a full read, the
// next poll does the full read again, because the status still has the
// failed family's old value.
func TestAFullReadWithAFailedCommandIsRepeated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		breakCommand(amp, balanceCommand, true)
		time.Sleep(settingsResync)
		client.poll(context.Background())

		breakCommand(amp, balanceCommand, false)
		amp.clear()
		client.poll(context.Background())

		mustMatch(t, holdsEveryRead(amp.sent()), true)
	})
}

// A command that has never responded is one this amp model does not
// support, so it does not make every poll a full read. The client tries
// it again at the next full read after settingsResync.
func TestACommandThatNeverRespondsWaitsForTheNextResync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)
		amp.answer(shutdownCommand, "0")
		breakCommand(amp, subCommand, true)
		client := amp.client(nil)
		client.poll(context.Background())

		amp.clear()
		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
	})
}

// An unreachable amp returns no readings, so each gauge disappears from
// the metrics scrape instead of keeping its last value.
func TestAnUnreachableAmpReportsNoReadings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		amp.mutex.Lock()
		amp.failing = http.StatusInternalServerError
		amp.mutex.Unlock()

		for range pollFailures {
			client.poll(context.Background())
		}

		mustMatch(t, client.Readings(), Readings{})
	})
}

// After a miss the amp may have rebooted, which drops its event
// subscriptions, so the next poll that succeeds renews both
// subscriptions. If the amp refuses the renewal, the client subscribes
// again and does a full read. If the amp accepts it, the poll sends only
// statusCommand and playerCommand.
func TestAPollAfterAMissRenewsTheSubscriptions(t *testing.T) {
	cases := []struct {
		name      string
		refused   bool
		subscribe int
		every     bool
	}{
		{"an amp that rebooted", true, 2, true},
		{"an amp that kept them", false, 1, false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				amp, events, client := subscribedAmpWithEvents(t)
				client.poll(context.Background())
				amp.mutex.Lock()
				amp.failing = http.StatusInternalServerError
				amp.mutex.Unlock()
				client.poll(context.Background())

				amp.mutex.Lock()
				amp.failing = 0
				amp.mutex.Unlock()
				events.mutex.Lock()
				events.refuseRenewals = one.refused
				events.mutex.Unlock()
				amp.clear()
				client.poll(context.Background())

				sent := amp.sent()
				mustMatch(t, countOf(sent, "SUBSCRIBE "+renderControlPath), one.subscribe)
				mustMatch(t, countOf(sent, "SUBSCRIBE "+avTransportPath), one.subscribe)
				mustMatch(t, holdsEveryRead(sent), one.every)
				mustMatch(t, client.subscribed(renderControlPath), true)
				mustMatch(t, client.subscribed(avTransportPath), true)
			})
		})
	}
}

func countOf(commands []string, command string) int {
	count := 0
	for index := slices.Index(commands, command); index >= 0; index = slices.Index(commands, command) {
		count++
		commands = commands[index+1:]
	}
	return count
}

// A command that responded once and then fails on every full read is
// retried for readRetries full reads. After that it waits for the next
// full read after settingsResync.
func TestACommandThatStopsRespondingWaitsForTheNextResync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		breakCommand(amp, balanceCommand, true)
		time.Sleep(settingsResync)
		for range readRetries {
			client.poll(context.Background())
		}

		amp.clear()
		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
	})
}

// The amp does not resend events that the client missed, and a
// subscription renewal does not send the current state. So after a miss
// or an outage the first poll that succeeds sets the volume from the
// player read, not from the last event.
func TestAPollAfterAMissSetsTheVolumeFromThePlayerRead(t *testing.T) {
	cases := []struct {
		name   string
		misses int
	}{
		{"one missed poll", 1},
		{"an outage", pollFailures},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				amp, events, client := subscribedAmpWithEvents(t)
				client.poll(context.Background())
				events.notify(t, renderControlPath, "0", renderControlBody("31", "0"))
				amp.mutex.Lock()
				amp.failing = http.StatusInternalServerError
				amp.mutex.Unlock()
				for range one.misses {
					client.poll(context.Background())
				}

				amp.mutex.Lock()
				amp.failing = 0
				amp.mutex.Unlock()
				amp.answer(playerCommand, strings.Replace(playerJSON, `"vol":"31"`, `"vol":"55"`, 1))
				client.poll(context.Background())

				mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 55)
				mustMatch(t, client.subscribed(renderControlPath), true)
			})
		})
	}
}

// An event that arrives after a miss and before the next successful
// poll has only the field that changed, here the mute. The poll still
// sets the volume from the player read.
func TestAnEventAfterAMissDoesNotSetTheVolume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, events, client := subscribedAmpWithEvents(t)
		client.poll(context.Background())
		events.notify(t, renderControlPath, "0", renderControlBody("31", "0"))
		amp.mutex.Lock()
		amp.failing = http.StatusInternalServerError
		amp.mutex.Unlock()
		client.poll(context.Background())
		events.notify(t, renderControlPath, "1", envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Mute channel="Master" val="1"/></InstanceID></Event>`))

		amp.mutex.Lock()
		amp.failing = 0
		amp.mutex.Unlock()
		amp.answer(playerCommand, strings.NewReplacer(`"vol":"31"`, `"vol":"55"`, `"mute":"0"`, `"mute":"1"`).Replace(playerJSON))
		client.poll(context.Background())

		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 55)
		mustMatch(t, client.State().Zones[equipment.MainZone].Mute, true)
	})
}

// An event that arrives during the full read after an outage is newer
// than that poll's read of the player, so the event's volume replaces
// the one the poll read.
func TestAnEventDuringThePollAfterAnOutageOverridesThePlayerRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, events, client := subscribedAmpWithEvents(t)
		client.poll(context.Background())
		events.notify(t, renderControlPath, "0", renderControlBody("31", "0"))
		amp.mutex.Lock()
		amp.failing = http.StatusInternalServerError
		amp.mutex.Unlock()
		for range pollFailures {
			client.poll(context.Background())
		}

		pushed := false
		amp.mutex.Lock()
		amp.failing = 0
		amp.onRead = func(command string) {
			if command == staticIPCommand && !pushed {
				pushed = true
				events.notify(t, renderControlPath, "2", renderControlBody("60", "0"))
			}
		}
		amp.mutex.Unlock()
		amp.answer(playerCommand, strings.Replace(playerJSON, `"vol":"31"`, `"vol":"55"`, 1))
		client.poll(context.Background())

		mustMatch(t, pushed, true)
		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 60)
	})
}

// After an outage, a failing command that had used up its readRetries
// gets them again, because an amp that rebooted may respond to it now.
func TestAnOutageRestoresTheRetriesOfAFailingCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		breakCommand(amp, balanceCommand, true)
		time.Sleep(settingsResync)
		for range readRetries {
			client.poll(context.Background())
		}
		amp.mutex.Lock()
		amp.failing = http.StatusInternalServerError
		amp.mutex.Unlock()
		for range pollFailures {
			client.poll(context.Background())
		}
		amp.mutex.Lock()
		amp.failing = 0
		amp.mutex.Unlock()
		client.poll(context.Background())

		amp.clear()
		client.poll(context.Background())

		mustMatch(t, holdsEveryRead(amp.sent()), true)
	})
}

// failOnePoll makes the fake fail the next poll, then respond again.
func failOnePoll(amp *fakeAmp, client *Client) {
	amp.mutex.Lock()
	amp.failing = http.StatusInternalServerError
	amp.mutex.Unlock()
	client.poll(context.Background())
	amp.mutex.Lock()
	amp.failing = 0
	amp.mutex.Unlock()
}

// A miss does not show that the amp rebooted, so it does not restore a
// failing command's readRetries.
func TestASingleMissDoesNotRestoreRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		breakCommand(amp, balanceCommand, true)
		time.Sleep(settingsResync)
		for range readRetries {
			client.poll(context.Background())
			failOnePoll(amp, client)
		}

		amp.clear()
		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
	})
}

// An amp that refuses the renewal after a miss has rebooted, so a
// failing command that had used up its readRetries gets them again.
func TestARefusedRenewalRestoresTheRetriesOfAFailingCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, events, client := subscribedAmpWithEvents(t)
		client.poll(context.Background())
		breakCommand(amp, balanceCommand, true)
		time.Sleep(settingsResync)
		for range readRetries {
			client.poll(context.Background())
		}
		events.mutex.Lock()
		events.refuseRenewals = true
		events.mutex.Unlock()
		failOnePoll(amp, client)
		client.poll(context.Background())

		amp.clear()
		client.poll(context.Background())

		mustMatch(t, holdsEveryRead(amp.sent()), true)
	})
}

// An event that arrives before the poll reads the player is older than
// that read, so the poll's volume replaces the event's.
func TestAnEventBeforeThePlayerReadDoesNotOverrideThePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, events, client := subscribedAmpWithEvents(t)
		client.poll(context.Background())
		pushed := false
		amp.mutex.Lock()
		amp.onRead = func(command string) {
			if command == statusCommand && !pushed {
				pushed = true
				events.notify(t, renderControlPath, "1", renderControlBody("40", "0"))
			}
		}
		amp.mutex.Unlock()
		amp.answer(playerCommand, strings.Replace(playerJSON, `"vol":"31"`, `"vol":"55"`, 1))

		client.poll(context.Background())

		mustMatch(t, pushed, true)
		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 55)
	})
}
