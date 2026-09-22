// The UPnP event path: the fake GENA server the client subscribes to,
// the parse of the real NOTIFY bodies, and the rule that a poll does
// not undo an event.

package wiim

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// fakeEvents is a device's UPnP event side: it takes a SUBSCRIBE, keeps
// the callback, and sends NOTIFYs to it on command.
type fakeEvents struct {
	server *httptest.Server

	mutex        sync.Mutex
	callbacks    map[string]string
	sids         map[string]string
	refuse       bool
	noSID        bool
	renewals     int
	unsubscribed []string
}

func startFakeEvents(t *testing.T) *fakeEvents {
	t.Helper()
	events := &fakeEvents{callbacks: map[string]string{}, sids: map[string]string{}}
	events.server = httptest.NewServer(http.HandlerFunc(events.handle))
	t.Cleanup(events.server.Close)
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
			f.mutex.Unlock()
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
		f.mutex.Unlock()
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

// notify sends one NOTIFY to the callback the client registered.
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
		t.Fatal(err)
	}
	req.Header.Set("SID", sid)
	req.Header.Set("SEQ", seq)
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

// listenerWithEvents builds a client whose UPnP base is the fake, and
// starts its event side.
func listenerWithEvents(t *testing.T) (*Client, *fakeEvents, <-chan equipment.Event) {
	t.Helper()
	events := startFakeEvents(t)
	got := make(chan equipment.Event, 16)
	client := NewClient("127.0.0.1:1", func(event equipment.Event) { got <- event })
	client.upnpBase = events.server.URL
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.startEvents(ctx)
	t.Cleanup(client.stopEvents)
	client.manageSubscriptions(ctx)
	return client, events, got
}

// envelope wraps a service's own document the way a NOTIFY does, with
// the inner document escaped into LastChange.
func envelope(inner string) string {
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(inner)
	return `<?xml version='1.0' encoding='UTF-8'?><e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0"><e:property><LastChange>` + escaped + `</LastChange></e:property></e:propertyset>`
}

// renderControlBody is the document RenderingControl pushes, taken from
// a live Amp.
func renderControlBody(volume, mute string) string {
	return envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Mute channel="Master" val="` + mute + `"/><Volume channel="Master" val="` + volume + `"/></InstanceID></Event>`)
}

// avTransportBody is the document AVTransport pushes, taken from a live
// Amp.
func avTransportBody(state string) string {
	return envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/"><InstanceID val="0"><TransportState val="` + state + `"/></InstanceID></Event>`)
}

// The subscribe takes both services and renews into the next term only
// when the grant is near its end.
func TestTheSubscriptionTakesBothServicesOnce(t *testing.T) {
	client, events, _ := listenerWithEvents(t)

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
}

// A volume event moves the equipment state and reaches the listener
// with no poll.
func TestAVolumeEventMovesTheState(t *testing.T) {
	client, events, got := listenerWithEvents(t)

	events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))

	select {
	case event := <-got:
		mustMatch(t, event.Field, equipment.EventVolume)
	case <-time.After(time.Second):
		t.Fatal("a volume event reported nothing")
	}
	mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 30)
}

// A change event names only what moved, so the field it leaves out
// keeps its value.
func TestAVolumeDeltaLeavesMuteStanding(t *testing.T) {
	client, events, _ := listenerWithEvents(t)

	events.notify(t, renderControlPath, "0", renderControlBody("30", "1"))
	mustMatch(t, client.State().Zones[equipment.MainZone].Mute, true)

	events.notify(t, renderControlPath, "1", envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Volume channel="Master" val="35"/></InstanceID></Event>`))
	mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 35)
	mustMatch(t, client.State().Zones[equipment.MainZone].Mute, true)
}

// A mute event reaches the listener as a mute.
func TestAMuteEventReachesTheListener(t *testing.T) {
	_, events, got := listenerWithEvents(t)

	events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))
	drainEvents(got)
	events.notify(t, renderControlPath, "1", envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Mute channel="Master" val="1"/></InstanceID></Event>`))

	select {
	case event := <-got:
		mustMatch(t, event.Field, equipment.EventMute)
	case <-time.After(time.Second):
		t.Fatal("a mute event reported nothing")
	}
}

// A transport event writes the play state in the word getPlayerStatus
// uses, so the snapshot does not name one state two ways.
func TestATransportEventWritesThePlayState(t *testing.T) {
	client, events, _ := listenerWithEvents(t)

	events.notify(t, avTransportPath, "0", avTransportBody("PLAYING"))
	mustMatch(t, client.Status().Playback.Status, "play")

	events.notify(t, avTransportPath, "1", avTransportBody("PAUSED_PLAYBACK"))
	mustMatch(t, client.Status().Playback.Status, "pause")

	events.notify(t, avTransportPath, "2", avTransportBody("STOPPED"))
	mustMatch(t, client.Status().Playback.Status, "stop")
}

// A NOTIFY for a SID the client did not take is not its own.
func TestANotifyForAnotherSubscriptionIsDropped(t *testing.T) {
	client, events, _ := listenerWithEvents(t)

	events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))
	mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 30)

	events.mutex.Lock()
	events.sids[renderControlPath] = "uuid:someone-else"
	events.mutex.Unlock()
	events.notify(t, renderControlPath, "1", renderControlBody("10", "0"))
	mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 30)
}

// A poll does not undo an event: while the subscription is live, the
// poll's own read of the volume is discarded.
func TestAPollDoesNotUndoAnEvent(t *testing.T) {
	client, events, _ := listenerWithEvents(t)
	events.notify(t, renderControlPath, "0", renderControlBody("30", "0"))
	events.notify(t, avTransportPath, "0", avTransportBody("PLAYING"))

	next := client.Status()
	next.Playback.Volume = 29
	next.Playback.Status = "stop"
	client.mergeEvents(&next)
	mustMatch(t, next.Playback.Volume, 30)
	mustMatch(t, next.Playback.Status, "play")
}

// With no subscription, the poll owns every field.
func TestAPollOwnsTheFieldsWithNoSubscription(t *testing.T) {
	client := NewClient("127.0.0.1:1", nil)

	next := client.Status()
	next.Playback.Volume = 29
	client.mergeEvents(&next)
	mustMatch(t, next.Playback.Volume, 29)
}

// A device that refuses the subscription leaves the client on the poll.
func TestARefusedSubscriptionLeavesThePoll(t *testing.T) {
	events := startFakeEvents(t)
	events.refuse = true
	client := NewClient("127.0.0.1:1", nil)
	client.upnpBase = events.server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.startEvents(ctx)
	defer client.stopEvents()

	client.manageSubscriptions(ctx)

	mustMatch(t, client.subscribed(renderControlPath), false)
	mustMatch(t, client.subscribed(avTransportPath), false)
}

// Stopping the client tells the device the callbacks are done.
func TestStoppingTheClientUnsubscribes(t *testing.T) {
	events := startFakeEvents(t)
	client := NewClient("127.0.0.1:1", nil)
	client.upnpBase = events.server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.startEvents(ctx)
	client.manageSubscriptions(ctx)

	client.stopEvents()

	events.mutex.Lock()
	unsubscribed := append([]string(nil), events.unsubscribed...)
	events.mutex.Unlock()
	mustMatch(t, len(unsubscribed), 2)
}

// A subscription reports both outcomes through the command counter the
// way every other request does.
func TestASubscriptionReportsACommand(t *testing.T) {
	events := startFakeEvents(t)
	reported := make(chan string, 8)
	client := NewClient("127.0.0.1:1", nil)
	client.Reporter = func(status string) { reported <- status }
	client.upnpBase = events.server.URL
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
}

// The parse pins the shape a live Amp pushes, including the escaped
// inner document the envelope carries.
func TestTheEventParseReadsTheLiveShapes(t *testing.T) {
	fields, ok := parseEvent(renderControlPath, []byte(renderControlBody("35", "1")))
	mustMatch(t, ok, true)
	mustMatch(t, *fields.volume, 35)
	mustMatch(t, *fields.mute, true)

	fields, ok = parseEvent(avTransportPath, []byte(avTransportBody("PLAYING")))
	mustMatch(t, ok, true)
	mustMatch(t, *fields.status, "play")

	if _, ok := parseEvent(renderControlPath, []byte("not xml")); ok {
		t.Fatal("a body that is not a propertyset parsed as one")
	}
}

// The timeout header a device sends is "Second-1800".
func TestTheTimeoutHeaderReads(t *testing.T) {
	mustMatch(t, timeoutSeconds("Second-1800"), 1800)
	mustMatch(t, timeoutSeconds("Second-300"), 300)
	mustMatch(t, timeoutSeconds("infinite"), subscribeSeconds)
	mustMatch(t, timeoutSeconds(""), subscribeSeconds)
}

// The UPnP base keeps the host and drops the control port.
func TestTheUPnPBaseKeepsTheHost(t *testing.T) {
	mustMatch(t, upnpBaseFor("10.0.0.1"), "http://10.0.0.1:"+upnpPort)
	mustMatch(t, upnpBaseFor("10.0.0.1:443"), "http://10.0.0.1:"+upnpPort)
	mustMatch(t, upnpBaseFor("amp.local:1234"), fmt.Sprintf("http://amp.local:%s", upnpPort))
}

// A renewal the device refuses drops the subscription, so the next
// pass subscribes it again.
func TestARejectedRenewalDropsTheSubscription(t *testing.T) {
	client, events, _ := listenerWithEvents(t)

	events.mutex.Lock()
	events.refuse = true
	events.mutex.Unlock()
	client.subscriptionsMutex.Lock()
	client.subscriptions[renderControlPath].expires = time.Now()
	client.subscriptionsMutex.Unlock()
	client.manageSubscriptions(context.Background())

	mustMatch(t, client.subscribed(renderControlPath), false)
}

// A 200 that names no SID is not a subscription.
func TestASubscriptionWithNoSIDIsRefused(t *testing.T) {
	events := startFakeEvents(t)
	events.noSID = true
	client := NewClient("127.0.0.1:1", nil)
	client.upnpBase = events.server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.startEvents(ctx)
	defer client.stopEvents()

	client.manageSubscriptions(ctx)
	mustMatch(t, client.subscribed(renderControlPath), false)
}

// A pass with no event listener reads nothing and reports nothing.
func TestManageSubscriptionsWithoutAListener(t *testing.T) {
	client := NewClient("127.0.0.1:1", nil)
	client.manageSubscriptions(context.Background())
	mustMatch(t, client.subscribed(renderControlPath), false)
}

// A grant with no usable number falls back to the default term.
func TestARecordedSubscriptionTakesTheDefaultTerm(t *testing.T) {
	client := NewClient("127.0.0.1:1", nil)
	client.recordSubscription(renderControlPath, "uuid:x", 0)
	client.subscriptionsMutex.Lock()
	sub := client.subscriptions[renderControlPath]
	client.subscriptionsMutex.Unlock()
	mustMatch(t, sub.sid, "uuid:x")
	if !sub.expires.After(time.Now().Add(subscribeSeconds / 2 * time.Second)) {
		t.Fatal("a grant with no number did not take the default term")
	}
}

// A body that is not a propertyset, one with no fields, and one for an
// unknown service all move nothing.
func TestAnEventThatCarriesNothingIsIgnored(t *testing.T) {
	client, events, _ := listenerWithEvents(t)

	events.notify(t, renderControlPath, "0", "not xml")
	events.notify(t, renderControlPath, "1", envelope(""))
	mustMatch(t, client.State().Zones[equipment.MainZone].Volume, equipment.Unknown)

	if _, ok := parseEvent("/upnp/event/other", []byte(renderControlBody("30", "0"))); ok {
		t.Fatal("an event for an unknown service parsed as one")
	}
	if _, ok := parseEvent(avTransportPath, []byte(envelope(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/"><InstanceID val="0"><TransportStatus val="OK"/></InstanceID></Event>`))); ok {
		t.Fatal("a transport event with no state parsed as one")
	}
}

// A volume field for another channel is not the master's.
func TestAVolumeFieldForAnotherChannelIsIgnored(t *testing.T) {
	if _, ok := parseRenderControl(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0"><Volume channel="Zone2" val="10"/></InstanceID></Event>`); ok {
		t.Fatal("a volume for another channel parsed as the master's")
	}
	if _, ok := parseRenderControl("not xml"); ok {
		t.Fatal("a body that is not a document parsed as one")
	}
}

// A transport state the driver does not know keeps the device's word
// in lower case rather than dropping it.
func TestAnUnknownTransportStateKeepsItsWord(t *testing.T) {
	mustMatch(t, playbackStatusFor("TRANSITIONING"), "transitioning")
	mustMatch(t, playbackStatusFor("NO_MEDIA_PRESENT"), "no_media_present")
}
