// When the client reads each family of the amp: the probe and the
// player on every poll, and the settings families only when a push
// cannot have told the client about a change.

package wiim

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// quietAmp is a fake amp that has answered its first poll, with no
// sleep timer running, and has the commands of that poll cleared. A
// test then reads only what the next poll sends.
func quietAmp(t *testing.T) (*fakeAmp, *Client) {
	t.Helper()
	amp := startFakeAmp(t)
	amp.answers[shutdownCommand] = "0"
	client := amp.client(nil)
	if !client.poll(context.Background()) {
		t.Fatal("the first poll did not answer")
	}
	amp.clear()
	return amp, client
}

func (f *fakeAmp) clear() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.commands = nil
}

func (f *fakeAmp) answer(command, body string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.answers[command] = body
}

// settingsReads are the reads of the families that no push carries.
var settingsReads = []string{
	staticIPCommand, outputModeCommand, outputListCommand, activeOutputCommand,
	balanceCommand, subCommand, spdifCommand, remoteStepCommand, buttonStepCommand,
	playModeEnable, playModeCommand, eqStatCommand, eqListCommand,
	shutdownCommand, powerModeCommand, alarmCommand + ":0", alarmCommand + ":1", alarmCommand + ":2",
	btPairCommand, btHistoryCommand, presetCommand, ledCommand, buttonsCommand, cecCommand,
}

// holdsEveryRead answers whether the commands hold every settings
// read and the track.
func holdsEveryRead(commands []string) bool {
	for _, command := range append([]string{metaCommand}, settingsReads...) {
		if !slices.Contains(commands, command) {
			return false
		}
	}
	return true
}

// After the first poll, a poll reads the probe and the player and
// nothing else, because nothing it would read again has a reason to
// differ.
func TestAQuietPollReadsOnlyTheProbeAndThePlayer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)

		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
	})
}

// The first poll reads every family, because the client holds nothing
// before it.
func TestTheFirstPollReadsEveryFamily(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)

		amp.client(nil).poll(context.Background())

		mustMatch(t, holdsEveryRead(amp.sent()), true)
	})
}

// The settings families are read again only once the backstop
// interval has passed since the last read of them.
func TestTheSettingsAreReadAgainAfterTheBackstop(t *testing.T) {
	cases := []struct {
		name  string
		after time.Duration
		want  bool
	}{
		{"before the interval", settingsResync - pollInterval, false},
		{"at the interval", settingsResync, true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				amp, client := quietAmp(t)

				time.Sleep(one.after)
				client.poll(context.Background())

				mustMatch(t, holdsEveryRead(amp.sent()), one.want)
			})
		})
	}
}

// An amp that comes back after the client called it unreachable may
// have rebooted or changed while it was gone, so the poll that finds it
// again reads every family.
func TestAnAmpThatComesBackIsReadAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		amp.mutex.Lock()
		amp.failing = http.StatusInternalServerError
		amp.mutex.Unlock()
		for range pollFailures {
			client.poll(context.Background())
		}
		mustMatch(t, client.State().Reachable, equipment.ConditionFalse)

		amp.mutex.Lock()
		amp.failing = 0
		amp.mutex.Unlock()
		amp.clear()
		client.poll(context.Background())

		mustMatch(t, holdsEveryRead(amp.sent()), true)
	})
}

// A new track in the player's report reads the track's details once.
func TestANewTrackReadsTheTrack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		amp.answer(playerCommand, strings.Replace(playerJSON, `"Title":"486176652061204369676172"`, `"Title":"4D6F6E6579"`, 1))
		amp.answer(metaCommand, strings.Replace(metaJSON, `"title":"Have a Cigar"`, `"title":"Money"`, 1))

		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand, metaCommand})
		mustMatch(t, client.Status().NowPlaying.Title, "Money")
	})
}

// A running sleep timer counts down on the amp, so each poll reads it
// while it runs and the zone's minutes follow the amp.
func TestARunningSleepTimerIsReadOnEachPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		amp.answer(shutdownCommand, "300")
		time.Sleep(settingsResync)
		client.poll(context.Background())
		amp.answer(shutdownCommand, "59")
		amp.clear()

		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand, shutdownCommand})
		mustMatch(t, client.State().Zones[equipment.MainZone].Sleep, 1)
	})
}

// A declared setting the client sends is read back at once, so the
// operator compares the next pass against the amp's own answer.
func TestASentSettingIsReadBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		amp.answer(ledCommand, "0")
		led := false

		mustSucceed(t, client.ApplySettings(Settings{Device: DeviceSettings{LED: &led}}))

		mustMatch(t, amp.sent(), []string{"LED_SWITCH_SET:0", ledCommand, buttonsCommand, cecCommand})
		mustMatch(t, *client.Settings().Device.LED, false)
	})
}

// The amp's clock, its temperatures, the radio's readings, and the
// track position change on their own between polls. They stay out of
// the status, so two polls that differ only in them leave it as it
// was, and the client reports them as readings instead.
func TestReadingsThatMoveOnTheirOwnStayOutOfTheStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)
		amp.answer(statusCommand, wifiStatusEx("12:00:00", "32", "-92", "480"))
		client := amp.client(nil)
		client.poll(context.Background())
		before := client.Status()

		amp.answer(statusCommand, wifiStatusEx("12:00:10", "33", "-90", "433"))
		amp.answer(playerCommand, strings.Replace(playerJSON, `"curpos":"1000"`, `"curpos":"11000"`, 1))
		client.poll(context.Background())

		mustMatch(t, client.Status(), before)
		mustMatch(t, client.Readings(), Readings{
			TemperatureCPU: intPtr(33), TemperatureBoard: intPtr(27),
			WiFiSignal: intPtr(-46), WiFiNoise: intPtr(-90), WiFiSNR: intPtr(35), WiFiRate: intPtr(433),
			RemoteBattery: intPtr(100), RemoteSignal: intPtr(-57),
		})
	})
}

// The status names none of the readings, so a reader of the CRD's
// status.wiim does not look for them there.
func TestTheStatusNamesNoReading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)
		amp.answer(statusCommand, wifiStatusEx("12:00:00", "32", "-92", "480"))
		client := amp.client(nil)
		client.poll(context.Background())

		body, err := json.Marshal(client.Status())
		mustSucceed(t, err)
		for _, key := range []string{`"time":"12:00:00"`, `"date"`, `"temperatureCpu"`, `"temperatureBoard"`, `"rssi"`, `"noise"`, `"snr"`, `"dataRate"`, `"battery"`, `"positionMs"`, `"offsetMs"`, `"sleepSeconds"`} {
			if strings.Contains(string(body), key) {
				t.Errorf("the status names %s: %s", key, body)
			}
		}
	})
}

// An amp on its wired port reports no radio readings.
func TestAWiredAmpReportsNoRadioReadings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)
		client := amp.client(nil)
		client.poll(context.Background())

		readings := client.Readings()
		mustMatch(t, readings.WiFiSignal, (*int)(nil))
		mustMatch(t, readings.WiFiRate, (*int)(nil))
		mustMatch(t, *readings.TemperatureCPU, 32)
	})
}

// wifiStatusEx is the fake's getStatusEx for an amp on Wi-Fi, with the
// clock, the CPU temperature, the noise, and the data rate a test
// moves.
func wifiStatusEx(clock, cpu, noise, rate string) string {
	return strings.NewReplacer(
		`"apcli0":"0.0.0.0"`, `"apcli0":"192.0.2.11"`,
		`"eth0":"192.0.2.10"`, `"eth0":"0.0.0.0"`,
		`"time":"12:00:00"`, `"time":"`+clock+`"`,
		`"temperature_cpu":"32"`, `"temperature_cpu":"`+cpu+`"`,
		`"RSSI":"0"`, `"RSSI":"-46"`,
		`"wlanSnr":"0"`, `"wlanSnr":"35"`,
		`"wlanNoise":"0"`, `"wlanNoise":"`+noise+`"`,
		`"wlanFreq":"0"`, `"wlanFreq":"5200"`,
		`"wlanDataRate":"0"`, `"wlanDataRate":"`+rate+`"`,
	).Replace(statusExJSON)
}

func intPtr(value int) *int { return &value }

// subscribedAmp is a fake amp with a UPnP side that takes the
// client's subscriptions. Each request to the UPnP side is logged among
// the amp's commands, so a test reads the order of the two.
func subscribedAmp(t *testing.T) (*fakeAmp, *Client) {
	t.Helper()
	amp, _, client := subscribedAmpWithEvents(t)
	return amp, client
}

// subscribedAmpWithEvents is subscribedAmp with the fake's UPnP side,
// for a test that changes how the UPnP side answers.
func subscribedAmpWithEvents(t *testing.T) (*fakeAmp, *fakeEvents, *Client) {
	t.Helper()
	amp := startFakeAmp(t)
	amp.answers[shutdownCommand] = "0"
	events := &fakeEvents{callbacks: map[string]string{}, sids: map[string]string{}}
	events.address = testNetwork.serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		amp.mutex.Lock()
		amp.commands = append(amp.commands, r.Method+" "+r.URL.Path)
		amp.mutex.Unlock()
		events.handle(w, r)
	}), false)
	client := amp.client(nil)
	client.upnpBase = "http://" + events.address
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.startEvents(ctx)
	t.Cleanup(client.stopEvents)
	return amp, events, client
}

// The first poll checks the amp's identity, opens the subscriptions,
// and only then reads the settings, so a change the amp pushes during
// the read is not lost.
func TestTheSubscriptionsOpenBeforeTheSettingsRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := subscribedAmp(t)

		client.poll(context.Background())

		sent := amp.sent()
		mustMatch(t, sent[:3], []string{statusCommand, "SUBSCRIBE " + renderControlPath, "SUBSCRIBE " + avTransportPath})
		mustMatch(t, holdsEveryRead(sent[3:]), true)
	})
}

// A subscription the client opens again may have missed events, so
// the poll that opens it reads every family. A poll that holds its
// subscriptions reads only the probe and the player.
func TestANewSubscriptionReadsEveryFamily(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := subscribedAmp(t)
		client.poll(context.Background())
		amp.clear()
		client.poll(context.Background())
		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})

		client.dropSubscription(renderControlPath)
		amp.clear()
		client.poll(context.Background())

		sent := amp.sent()
		mustMatch(t, sent[:2], []string{statusCommand, "SUBSCRIBE " + renderControlPath})
		mustMatch(t, holdsEveryRead(sent), true)
	})
}

// An amp the client called unreachable may only have been cut off, and
// then it still holds the subscriptions, so the client keeps them and
// renews them when the amp answers, rather than open two more on the
// amp. An amp that rebooted refuses the renewal and is subscribed
// again. Either way the poll reads every family.
func TestAnAmpThatComesBackKeepsOrReopensItsSubscriptions(t *testing.T) {
	cases := []struct {
		name      string
		refused   bool
		subscribe int
	}{
		{"an amp that was cut off", false, 1},
		{"an amp that rebooted", true, 2},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				amp, events, client := subscribedAmpWithEvents(t)
				client.poll(context.Background())
				amp.mutex.Lock()
				amp.failing = http.StatusInternalServerError
				amp.mutex.Unlock()
				for range pollFailures {
					client.poll(context.Background())
				}
				mustMatch(t, client.subscribed(renderControlPath), true)

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
				mustMatch(t, holdsEveryRead(sent), true)
				mustMatch(t, client.subscribed(renderControlPath), true)
			})
		})
	}
}
