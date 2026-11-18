package wiim

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// wiimAmpDescription is a WiiM Amp's description, trimmed to the
// fields around the ones the client reads.
const wiimAmpDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <friendlyName>Den Amp</friendlyName>
    <manufacturer>Linkplay Technology Inc.</manufacturer>
    <modelName>WiiM Amp</modelName>
    <modelNumber>V01-Sep 22 2026</modelNumber>
  </device>
</root>`

// describedAmp returns a fake amp whose UPnP server responds to each
// description request with the status code and counts the requests.
func describedAmp(t *testing.T, code int) (*fakeAmp, *Client, *atomic.Int64) {
	t.Helper()
	reads := &atomic.Int64{}
	upnp := testNetwork.serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		mustMatch(t, r.URL.Path, descriptionPath)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(wiimAmpDescription))
	}), false)
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.upnpBase = "http://" + upnp
	return amp, client, reads
}

func TestTheClientReadsTheModelAfterAnAnsweredPoll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		_, client, reads := describedAmp(t, http.StatusOK)
		client.poll(context.Background())

		client.describe(context.Background())
		client.describe(context.Background())

		mustMatch(t, client.State().Model, "WiiM Amp")
		mustMatch(t, client.State().Manufacturer, "Linkplay Technology Inc.")
		mustMatch(t, reads.Load(), int64(1))
	})
}

func TestTheClientReadsNoModelBeforeTheAmpAnswers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		_, client, reads := describedAmp(t, http.StatusOK)

		client.describe(context.Background())

		mustMatch(t, reads.Load(), int64(0))
		mustMatch(t, client.State().Model, "")
	})
}

func TestTheClientReadsNoModelFromAnotherBrand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		amp, client, reads := describedAmp(t, http.StatusOK)
		amp.answers[statusCommand] = strings.Replace(statusExJSON, `"project":"WiiM_Amp_4layer"`, `"project":"ARYLIC_A50TE"`, 1)
		client.poll(context.Background())

		client.describe(context.Background())

		mustMatch(t, reads.Load(), int64(0))
	})
}

func TestTheClientWaitsBeforeItReadsAFailedDescriptionAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		_, client, reads := describedAmp(t, http.StatusNotFound)
		client.poll(context.Background())

		client.describe(context.Background())
		client.describe(context.Background())

		mustMatch(t, reads.Load(), int64(1))
		mustMatch(t, client.State().Model, "")
	})
}
