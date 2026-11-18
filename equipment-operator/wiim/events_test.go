package wiim

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// fakeEvents is the GENA side of a device. It accepts SUBSCRIBE
// requests, records each callback URL, and sends a NOTIFY to it when a
// test calls notify.
type fakeEvents struct {
	address string

	mutex               sync.Mutex
	callbacks           map[string]string
	sids                map[string]string
	refuse              bool
	noSID               bool
	closeAfterSubscribe bool
	// refuseRenewals makes the fake respond 412 to a renewal, as a
	// device that rebooted does for a SID it no longer has.
	refuseRenewals bool
	renewals       int
	unsubscribed   []string
}

func startFakeEvents(t *testing.T) *fakeEvents {
	t.Helper()
	events := &fakeEvents{callbacks: map[string]string{}, sids: map[string]string{}}
	events.address = testNetwork.serve(t, http.HandlerFunc(events.handle), false)
	return events
}

func (f *fakeEvents) handle(w http.ResponseWriter, r *http.Request) {
	f.mutex.Lock()
	refuse := f.refuse
	f.mutex.Unlock()
	switch r.Method {
	case "SUBSCRIBE":
		if refuse {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if sid := r.Header.Get("SID"); sid != "" {
			f.mutex.Lock()
			f.renewals++
			refuseRenewal := f.refuseRenewals
			f.mutex.Unlock()
			if refuseRenewal {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			w.Header().Set("SID", sid)
			w.Header().Set("TIMEOUT", "Second-1800")
			return
		}
		f.mutex.Lock()
		noSID := f.noSID
		f.mutex.Unlock()
		if noSID {
			w.Header().Set("TIMEOUT", "Second-1800")
			return
		}
		sid := "uuid:" + r.URL.Path
		callback := strings.Trim(r.Header.Get("CALLBACK"), "<>")
		f.mutex.Lock()
		f.callbacks[r.URL.Path] = callback
		f.sids[r.URL.Path] = sid
		closeConn := f.closeAfterSubscribe
		f.mutex.Unlock()
		if closeConn {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nSID: %s\r\nTIMEOUT: Second-1800\r\nConnection: keep-alive\r\nContent-Length: 0\r\n\r\n", sid)
				conn.Close()
				return
			}
		}
		w.Header().Set("SID", sid)
		w.Header().Set("TIMEOUT", "Second-1800")
	case "UNSUBSCRIBE":
		f.mutex.Lock()
		f.unsubscribed = append(f.unsubscribed, r.URL.Path)
		delete(f.callbacks, r.URL.Path)
		f.mutex.Unlock()
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (c *Client) subscribed(path string) bool {
	c.subscriptionsMutex.Lock()
	defer c.subscriptionsMutex.Unlock()
	sub, ok := c.subscriptions[path]
	return ok && sub != nil && sub.sid != ""
}

func (f *fakeEvents) notify(t *testing.T, path, seq, body string) {
	t.Helper()
	f.mutex.Lock()
	callback, sid := f.callbacks[path], f.sids[path]
	f.mutex.Unlock()
	if callback == "" {
		t.Fatalf("the client registered no callback for %s", path)
	}
	req, err := http.NewRequest("NOTIFY", callback, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the NOTIFY request: %v", err)
	}
	req.Header.Set("SID", sid)
	req.Header.Set("SEQ", seq)
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	device := &http.Client{Transport: &http.Transport{DialContext: testNetwork.dial, DisableKeepAlives: true}}
	resp, err := device.Do(req)
	if err != nil {
		t.Fatalf("sending the NOTIFY request: %v", err)
	}
	resp.Body.Close()
}

// subscribedClient returns a client that has started its callback
// listener and subscribed to both services of the fake.
func subscribedClient(t *testing.T) (*Client, *fakeEvents, <-chan equipment.Event) {
	t.Helper()
	events := startFakeEvents(t)
	got := make(chan equipment.Event, 16)
	client := testClient("127.0.0.1:1", func(event equipment.Event) { got <- event })
	client.upnpBase = "http://" + events.address
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.startEvents(ctx)
	t.Cleanup(client.stopEvents)
	client.manageSubscriptions(ctx)
	return client, events, got
}

// envelope wraps a service's document in a NOTIFY body, with the
// document escaped inside LastChange.
func envelope(inner string) string {
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(inner)
	return `<?xml version='1.0' encoding='UTF-8'?><e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0"><e:property><LastChange>` + escaped + `</LastChange></e:property></e:propertyset>`
}

// renderControlBody is a RenderingControl NOTIFY body in the form that
// a WiiM Amp sends.
func renderControlBody(volume, mute string) string {
	return envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Mute channel="Master" val="` + mute + `"/><Volume channel="Master" val="` + volume + `"/></InstanceID></Event>`)
}

// avTransportBody is an AVTransport NOTIFY body in the form that a WiiM
// Amp sends.
func avTransportBody(state string) string {
	return envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/"><InstanceID val="0"><TransportState val="` + state + `"/></InstanceID></Event>`)
}

// The client subscribes to both services, and renews a subscription
// only when its grant is near its end.
func TestTheSubscriptionTakesBothServicesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)

		mustMatch(t, client.subscribed(renderControlPath), true)
		mustMatch(t, client.subscribed(avTransportPath), true)
		events.mutex.Lock()
		renewals := events.renewals
		events.mutex.Unlock()
		mustMatch(t, renewals, 0)

		client.manageSubscriptions(context.Background())
		events.mutex.Lock()
		renewals = events.renewals
		events.mutex.Unlock()
		mustMatch(t, renewals, 0)

		client.subscriptionsMutex.Lock()
		client.subscriptions[renderControlPath].expires = time.Now()
		client.subscriptionsMutex.Unlock()
		client.manageSubscriptions(context.Background())
		events.mutex.Lock()
		renewals = events.renewals
		events.mutex.Unlock()
		mustMatch(t, renewals, 1)
	})
}

// A volume event changes the state and reaches the listener without a
// poll.
func TestAVolumeEventMovesTheState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, got := subscribedClient(t)

		events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))

		select {
		case event := <-got:
			mustMatch(t, event.Field, equipment.EventVolume)
		case <-time.After(time.Second):
			t.Fatal("no event reached the listener within 1 second of a volume NOTIFY")
		}
		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 30)
	})
}

// An event lists only the values that changed, so a value it leaves out
// keeps its earlier value.
func TestAVolumeEventLeavesMuteUnchanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)

		events.notify(t, renderControlPath, "0", renderControlBody("30", "1"))
		mustMatch(t, client.State().Zones[equipment.MainZone].Mute, true)

		events.notify(t, renderControlPath, "1", envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Volume channel="Master" val="35"/></InstanceID></Event>`))
		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 35)
		mustMatch(t, client.State().Zones[equipment.MainZone].Mute, true)
	})
}

func TestAMuteEventReachesTheListener(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, events, got := subscribedClient(t)

		events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))
		drainEvents(got)
		events.notify(t, renderControlPath, "1", envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Mute channel="Master" val="1"/></InstanceID></Event>`))

		select {
		case event := <-got:
			mustMatch(t, event.Field, equipment.EventMute)
		case <-time.After(time.Second):
			t.Fatal("no event reached the listener within 1 second of a mute NOTIFY")
		}
	})
}

// A transport event sets the play state to the word that getPlayerStatus
// uses for it.
func TestATransportEventWritesThePlayState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)

		events.notify(t, avTransportPath, "0", avTransportBody("PLAYING"))
		mustMatch(t, client.Status().Playback.Status, "play")

		events.notify(t, avTransportPath, "1", avTransportBody("PAUSED_PLAYBACK"))
		mustMatch(t, client.Status().Playback.Status, "pause")

		events.notify(t, avTransportPath, "2", avTransportBody("STOPPED"))
		mustMatch(t, client.Status().Playback.Status, "stop")
	})
}

// The client ignores a NOTIFY whose SID matches none of its
// subscriptions.
func TestANotifyForAnotherSubscriptionIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)

		events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))
		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 30)

		events.mutex.Lock()
		events.sids[renderControlPath] = "uuid:someone-else"
		events.mutex.Unlock()
		events.notify(t, renderControlPath, "1", renderControlBody("10", "0"))
		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 30)
	})
}

// When an event arrives after a poll has read the device but before the
// poll publishes, the poll does not overwrite the event's value with
// its older read.
func TestAPollDoesNotUndoAnEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)
		mark := client.eventMark()
		events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))
		events.notify(t, avTransportPath, "0", avTransportBody("PLAYING"))

		next := client.Status()
		next.Playback.Volume = 29
		next.Playback.Status = "stop"
		client.publishSince(next, mark)

		mustMatch(t, client.Status().Playback.Volume, 30)
		mustMatch(t, client.Status().Playback.Status, "play")
	})
}

// A poll publishes each field that no event wrote after the poll's
// mark, because the poll's read is newer.
func TestAPollPublishesTheFieldsNoLaterEventWrote(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)
		events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))
		mark := client.eventMark()
		events.notify(t, avTransportPath, "1", avTransportBody("PLAYING"))

		next := client.Status()
		next.Playback.Volume = 29
		next.Playback.Status = "stop"
		client.publishSince(next, mark)

		mustMatch(t, client.Status().Playback.Volume, 29)
		mustMatch(t, client.Status().Playback.Status, "play")
	})
}

// When the device refuses to subscribe, the client has no
// subscription.
func TestARefusedSubscriptionLeavesTheClientOnThePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := startFakeEvents(t)
		events.refuse = true
		client := testClient("127.0.0.1:1", nil)
		client.upnpBase = "http://" + events.address
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client.startEvents(ctx)
		defer client.stopEvents()

		client.manageSubscriptions(ctx)

		mustMatch(t, client.subscribed(renderControlPath), false)
		mustMatch(t, client.subscribed(avTransportPath), false)
	})
}

func TestStoppingTheClientUnsubscribes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := startFakeEvents(t)
		client := testClient("127.0.0.1:1", nil)
		client.upnpBase = "http://" + events.address
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client.startEvents(ctx)
		client.manageSubscriptions(ctx)

		client.stopEvents()

		events.mutex.Lock()
		unsubscribed := append([]string(nil), events.unsubscribed...)
		events.mutex.Unlock()
		mustMatch(t, len(unsubscribed), 2)
	})
}

// A SUBSCRIBE reports CommandOK or CommandFailed, as every other
// request to the device does.
func TestASubscriptionReportsACommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := startFakeEvents(t)
		reported := make(chan string, 8)
		client := testClient("127.0.0.1:1", nil)
		client.Reporter = func(status string) { reported <- status }
		client.upnpBase = "http://" + events.address
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client.startEvents(ctx)
		defer client.stopEvents()

		client.manageSubscriptions(ctx)
		mustMatch(t, <-reported, CommandOK)
		mustMatch(t, <-reported, CommandOK)

		events.mutex.Lock()
		events.refuse = true
		events.mutex.Unlock()
		client.dropSubscription(renderControlPath)
		client.manageSubscriptions(ctx)
		mustMatch(t, <-reported, CommandFailed)
	})
}

// parseEvent reads the bodies that a WiiM Amp sends, with the service's
// document escaped inside LastChange.
func TestParseEventReadsWiiMAmpBodies(t *testing.T) {
	fields, ok := parseEvent(renderControlPath, []byte(renderControlBody("35", "1")))
	mustMatch(t, ok, true)
	mustMatch(t, *fields.volume, 35)
	mustMatch(t, *fields.mute, true)

	fields, ok = parseEvent(avTransportPath, []byte(avTransportBody("PLAYING")))
	mustMatch(t, ok, true)
	mustMatch(t, *fields.status, "play")

	if _, ok := parseEvent(renderControlPath, []byte("not xml")); ok {
		t.Fatal("parseEvent accepted a body that is not a propertyset, want it refused")
	}
}

func TestTheTimeoutHeaderReads(t *testing.T) {
	mustMatch(t, timeoutSeconds("Second-1800"), 1800)
	mustMatch(t, timeoutSeconds("Second-300"), 300)
	mustMatch(t, timeoutSeconds("infinite"), subscribeSeconds)
	mustMatch(t, timeoutSeconds(""), subscribeSeconds)
}

func TestTheUPnPBaseKeepsTheHost(t *testing.T) {
	mustMatch(t, upnpBaseFor("10.0.0.1"), "http://10.0.0.1:"+upnpPort)
	mustMatch(t, upnpBaseFor("10.0.0.1:443"), "http://10.0.0.1:"+upnpPort)
	mustMatch(t, upnpBaseFor("amp.local:1234"), fmt.Sprintf("http://amp.local:%s", upnpPort))
}

// When the device refuses a renewal, the client drops the subscription
// and the next manageSubscriptions subscribes again.
func TestARejectedRenewalDropsTheSubscription(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)

		events.mutex.Lock()
		events.refuse = true
		events.mutex.Unlock()
		client.subscriptionsMutex.Lock()
		client.subscriptions[renderControlPath].expires = time.Now()
		client.subscriptionsMutex.Unlock()
		client.manageSubscriptions(context.Background())

		mustMatch(t, client.subscribed(renderControlPath), false)
	})
}

// A 200 response with no SID header is not a subscription.
func TestASubscriptionWithNoSIDIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := startFakeEvents(t)
		events.noSID = true
		client := testClient("127.0.0.1:1", nil)
		client.upnpBase = "http://" + events.address
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client.startEvents(ctx)
		defer client.stopEvents()

		client.manageSubscriptions(ctx)
		mustMatch(t, client.subscribed(renderControlPath), false)
	})
}

// Without a callback listener, manageSubscriptions subscribes to
// nothing.
func TestManageSubscriptionsWithoutAListener(t *testing.T) {
	client := testClient("127.0.0.1:1", nil)
	client.manageSubscriptions(context.Background())
	mustMatch(t, client.subscribed(renderControlPath), false)
}

// recordSubscription uses subscribeSeconds for a grant of zero
// seconds.
func TestARecordedSubscriptionTakesTheDefaultTerm(t *testing.T) {
	client := testClient("127.0.0.1:1", nil)
	client.recordSubscription(renderControlPath, "uuid:x", 0)
	client.subscriptionsMutex.Lock()
	sub := client.subscriptions[renderControlPath]
	client.subscriptionsMutex.Unlock()
	mustMatch(t, sub.sid, "uuid:x")
	if !sub.expires.After(time.Now().Add(subscribeSeconds / 2 * time.Second)) {
		t.Fatal("a grant of 0 seconds expires sooner than half of subscribeSeconds, want the default term")
	}
}

// A body that is not a propertyset, a body with no values, and a body
// from an unknown service change nothing.
func TestAnEventWithNoValuesIsIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, events, _ := subscribedClient(t)

		events.notify(t, renderControlPath, "0", "not xml")
		events.notify(t, renderControlPath, "1", envelope(""))
		mustMatch(t, client.State().Zones[equipment.MainZone].Volume, equipment.Unknown)

		if _, ok := parseEvent("/upnp/event/other", []byte(renderControlBody("30", "0"))); ok {
			t.Fatal("parseEvent accepted an unknown service path, want it refused")
		}
		if _, ok := parseEvent(avTransportPath, []byte(envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/"><InstanceID val="0"><TransportStatus val="OK"/></InstanceID></Event>`))); ok {
			t.Fatal("parseEvent accepted a transport event with no TransportState, want it refused")
		}
	})
}

// The client ignores a volume for any channel except Master, and a
// document that is not XML.
func TestAVolumeFieldForAnotherChannelIsIgnored(t *testing.T) {
	if _, ok := parseRenderControl(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Volume channel="Zone2" val="10"/></InstanceID></Event>`); ok {
		t.Fatal("parseRenderControl accepted a volume of channel Zone2, want only Master")
	}
	if _, ok := parseRenderControl("not xml"); ok {
		t.Fatal("parseRenderControl accepted a body that is not XML, want it refused")
	}
}

// A transport state that the driver does not know becomes the device's
// word in lower case.
func TestAnUnknownTransportStateKeepsItsWord(t *testing.T) {
	mustMatch(t, playbackStatusFor("TRANSITIONING"), "transitioning")
	mustMatch(t, playbackStatusFor("NO_MEDIA_PRESENT"), "no_media_present")
}

// A WiiM closes the connection after it responds to a SUBSCRIBE,
// although its response says keep-alive. So each subscription must
// open its own connection.
func TestASubscriptionOpensItsOwnConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := startFakeEvents(t)
		events.closeAfterSubscribe = true
		client := testClient("127.0.0.1:1", nil)
		client.upnpBase = "http://" + events.address
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client.startEvents(ctx)
		defer client.stopEvents()

		client.manageSubscriptions(ctx)
		mustMatch(t, client.subscribed(renderControlPath), true)
		mustMatch(t, client.subscribed(avTransportPath), true)
	})
}

// TestASubscriptionOpensItsOwnConnection can pass with a reused
// connection, because Go's transport can retry a request that failed on
// a reused connection. So this test checks the setting directly.
func TestTheSubscriptionClientOpensEachConnection(t *testing.T) {
	client := testClient("127.0.0.1:1", nil)
	transport, ok := client.upnp.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the subscription client transport is %T, want *http.Transport", client.upnp.Transport)
	}
	mustMatch(t, transport.DisableKeepAlives, true)
}
