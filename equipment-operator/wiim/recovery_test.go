// How the client recovers from a read that failed: a track read that
// did not answer, a full read with a failed read in it, and an amp that
// missed polls and may have rebooted.

package wiim

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// newTrack makes the fake's player report another track.
func newTrack(amp *fakeAmp) {
	amp.answer(playerCommand, strings.Replace(playerJSON, `"Title":"486176652061204369676172"`, `"Title":"4D6F6E6579"`, 1))
}

func breakCommand(amp *fakeAmp, command string, broken bool) {
	amp.mutex.Lock()
	defer amp.mutex.Unlock()
	amp.broken = map[string]bool{command: broken}
}

// A track read whose request fails is owed, so the next poll reads the
// track again, and the poll after the answer does not.
func TestAFailedTrackReadIsReadAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		newTrack(amp)
		breakCommand(amp, metaCommand, true)
		client.poll(context.Background())

		breakCommand(amp, metaCommand, false)
		amp.answer(metaCommand, strings.Replace(metaJSON, `"title":"Have a Cigar"`, `"title":"Money"`, 1))
		amp.clear()
		client.poll(context.Background())
		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand, metaCommand})
		mustMatch(t, client.Status().NowPlaying.Title, "Money")

		amp.clear()
		client.poll(context.Background())
		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
	})
}

// A track read that keeps failing is tried on a bounded number of
// polls, not on every poll.
func TestATrackReadThatKeepsFailingStops(t *testing.T) {
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

// Any answer to the track read settles it, even one that names another
// title than the player does, the way line-in, optical, and internet
// radio answer. Otherwise the track would be read on every poll.
func TestATrackReadThatAnswersIsSettled(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{"no track", "Failed"},
		{"another title", strings.Replace(metaJSON, `"title":"Have a Cigar"`, `"title":"unknow"`, 1)},
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

// A full read in which a command that answered before fails is done
// again on the next poll, because that family still holds what it held
// before.
func TestAFullReadWithAFailedReadIsDoneAgain(t *testing.T) {
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

// A command that has never answered is one the model does not serve,
// so it does not make every poll a full read: the next full read is the
// backstop's.
func TestACommandThatNeverAnswersWaitsForTheBackstop(t *testing.T) {
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

// An amp the client called unreachable reports no readings, so each
// gauge leaves the scrape instead of holding its last value.
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

// An amp that misses one poll may have rebooted, and a reboot drops
// its subscriptions, so the first poll it answers renews both at once.
// An amp that refuses the renewal gets new subscriptions and a read of
// every family. An amp that takes it holds the subscriptions, and the
// poll reads only the probe and the player.
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

// A command that answered once and then fails on every full read is
// given a bounded number of full reads, then left to the backstop, so
// it does not make every poll a full read.
func TestACommandThatStopsAnsweringWaitsForTheBackstop(t *testing.T) {
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

// The amp sends no event it missed while the client could not reach it,
// and a renewal brings no full state, so the first poll the amp answers
// after a miss takes the volume from the poll and not from the events.
func TestAPollAfterAMissTakesTheVolumeFromTheAmp(t *testing.T) {
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

// An event that arrives after a miss and before the next answered poll
// carries only what it moved, here the mute, so the poll still takes
// the volume the amp reports.
func TestAnEventAfterAMissLeavesTheVolumeToThePoll(t *testing.T) {
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

// An event that arrives while the poll after an outage reads every
// family is newer than the poll's read of the player, so it stands.
func TestAnEventDuringThePollAfterAnOutageStands(t *testing.T) {
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

// A command left to the backstop gets its retries again after an
// outage, because an amp that rebooted may answer it once it is up.
func TestAnOutageGivesASilentCommandItsRetriesAgain(t *testing.T) {
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
