// The tests of which reads each poll sends. Every poll reads
// getStatusEx and getPlayerStatus. A poll reads the setting families
// only when the amp may have changed them without an event reaching
// the client.

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

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// quietAmp returns a fake amp after its first poll, with no sleep timer
// running and the commands of that poll cleared.
func quietAmp(t *testing.T) (*fakeAmp, *Client) {
	t.Helper()
	amp := startFakeAmp(t)
	amp.answers[shutdownCommand] = "0"
	client := amp.client(nil)
	if !client.poll(context.Background()) {
		t.Fatal("the first poll returned false, want true because getStatusEx reports the declared UUID")
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

// settingsReads are the commands that read the setting families.
var settingsReads = []string{
	staticIPCommand, outputModeCommand, outputListCommand, activeOutputCommand,
	balanceCommand, subCommand, spdifCommand, remoteStepCommand, buttonStepCommand,
	playModeEnable, playModeCommand, eqStatCommand, eqListCommand,
	shutdownCommand, powerModeCommand, alarmCommand + ":0", alarmCommand + ":1", alarmCommand + ":2",
	btPairCommand, btHistoryCommand, presetCommand, ledCommand, buttonsCommand, cecCommand,
}

// holdsEveryRead reports whether commands include every settings read
// and the track metadata read.
func holdsEveryRead(commands []string) bool {
	for _, command := range append([]string{metaCommand}, settingsReads...) {
		if !slices.Contains(commands, command) {
			return false
		}
	}
	return true
}

func TestAQuietPollReadsOnlyTheStatusAndThePlayer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)

		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand})
	})
}

func TestTheFirstPollReadsEveryFamily(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)

		amp.client(nil).poll(context.Background())

		mustMatch(t, holdsEveryRead(amp.sent()), true)
	})
}

// settingsResync is a backstop for a change that no event reported.
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

// An amp that was unreachable may have rebooted or changed, so the
// first poll after it returns reads every family.
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

func TestANewTrackReadsTheTrack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := quietAmp(t)
		amp.answer(playerCommand, strings.Replace(playerJSON, `"Title":"4C616E7465726E20536F6E67"`, `"Title":"4D6F6E6579"`, 1))
		amp.answer(metaCommand, strings.Replace(metaJSON, `"title":"Lantern Song"`, `"title":"Second Song"`, 1))

		client.poll(context.Background())

		mustMatch(t, amp.sent(), []string{statusCommand, playerCommand, metaCommand})
		mustMatch(t, client.Status().NowPlaying.Title, "Second Song")
	})
}

// No event reports the countdown of a sleep timer, so each poll reads
// the timer while it runs.
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

// The client reads a setting back right after it sends it, so the
// operator's next pass compares the declared value with the amp's.
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

// The amp's clock, temperatures, radio readings, and track position
// change on every poll. The client returns them from Readings, not
// Status, so they do not cause a status write.
func TestReadingsThatChangeOnTheirOwnStayOutOfTheStatus(t *testing.T) {
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

// status.wiim in the CRD does not include the reading fields.
func TestTheStatusHasNoReadingFields(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)
		amp.answer(statusCommand, wifiStatusEx("12:00:00", "32", "-92", "480"))
		client := amp.client(nil)
		client.poll(context.Background())

		body, err := json.Marshal(client.Status())
		mustSucceed(t, err)
		for _, key := range []string{`"time":"12:00:00"`, `"date"`, `"temperatureCpu"`, `"temperatureBoard"`, `"rssi"`, `"noise"`, `"snr"`, `"dataRate"`, `"battery"`, `"positionMs"`, `"offsetMs"`, `"sleepSeconds"`} {
			if strings.Contains(string(body), key) {
				t.Errorf("the status is %s, want no %s", body, key)
			}
		}
	})
}

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

// wifiStatusEx returns the fake's getStatusEx response for an amp on
// Wi-Fi, with the given clock, CPU temperature, noise, and data rate.
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

// subscribedAmp returns a fake amp with a UPnP server that accepts the
// client's subscriptions. The fake logs each UPnP request among the
// amp's commands, so a test can check the order of both.
func subscribedAmp(t *testing.T) (*fakeAmp, *Client) {
	t.Helper()
	amp, _, client := subscribedAmpWithEvents(t)
	return amp, client
}

// subscribedAmpWithEvents is subscribedAmp that also returns the fake
// UPnP server, for a test that changes how that server responds.
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

// The first poll checks the amp's identity and opens the subscriptions
// before it reads the settings, so an event during the read is not
// lost.
func TestTheSubscriptionsOpenBeforeTheSettingsRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp, client := subscribedAmp(t)

		client.poll(context.Background())

		sent := amp.sent()
		mustMatch(t, sent[:3], []string{statusCommand, "SUBSCRIBE " + renderControlPath, "SUBSCRIBE " + avTransportPath})
		mustMatch(t, holdsEveryRead(sent[3:]), true)
	})
}

// A subscription that the client opens again may have missed events,
// so the poll that opens it reads every family.
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

// An amp that only lost its connection still has the subscriptions, so
// the client renews them instead of opening duplicates. An amp that
// rebooted refuses the renewal, and the client subscribes again. In
// both cases the poll reads every family.
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
