// The model the client reads from the amp's UPnP description.

package wiim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// wiimAmpDescription is the root of a WiiM Amp's description, cut to
// the fields the client reads and the ones around them.
const wiimAmpDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <friendlyName>Den Amp</friendlyName>
    <manufacturer>Linkplay Technology Inc.</manufacturer>
    <modelName>WiiM Amp</modelName>
    <modelNumber>V01-Sep 22 2026</modelNumber>
  </device>
</root>`

// describedAmp is a fake amp whose UPnP side answers its description
// with code, and counts the reads.
func describedAmp(t *testing.T, code int) (*fakeAmp, *Client, *atomic.Int64) {
	t.Helper()
	reads := &atomic.Int64{}
	upnp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		mustMatch(t, r.URL.Path, descriptionPath)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(wiimAmpDescription))
	}))
	t.Cleanup(upnp.Close)
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.upnpBase = upnp.URL
	return amp, client, reads
}

func TestTheClientReadsTheModelAfterAnAnsweredPoll(t *testing.T) {
	t.Parallel()
	_, client, reads := describedAmp(t, http.StatusOK)
	client.poll(context.Background())

	client.describe(context.Background())
	client.describe(context.Background())

	mustMatch(t, client.State().Model, "WiiM Amp")
	mustMatch(t, client.State().Manufacturer, "Linkplay Technology Inc.")
	mustMatch(t, reads.Load(), int64(1))
}

func TestTheClientReadsNoModelBeforeTheAmpAnswers(t *testing.T) {
	t.Parallel()
	_, client, reads := describedAmp(t, http.StatusOK)

	client.describe(context.Background())

	mustMatch(t, reads.Load(), int64(0))
	mustMatch(t, client.State().Model, "")
}

// A device that is not a WiiM gets getStatusEx and nothing else, so
// the client never reads its description.
func TestTheClientReadsNoModelFromAnotherBrand(t *testing.T) {
	t.Parallel()
	amp, client, reads := describedAmp(t, http.StatusOK)
	amp.answers[statusCommand] = strings.Replace(statusExJSON, `"project":"WiiM_Amp_4layer"`, `"project":"ARYLIC_A50TE"`, 1)
	client.poll(context.Background())

	client.describe(context.Background())

	mustMatch(t, reads.Load(), int64(0))
}

// A failed read waits out describeRetryInterval before the next one,
// so an amp that does not serve its description is not asked on every
// poll.
func TestTheClientWaitsBeforeItReadsAFailedDescriptionAgain(t *testing.T) {
	t.Parallel()
	_, client, reads := describedAmp(t, http.StatusNotFound)
	client.poll(context.Background())

	client.describe(context.Background())
	client.describe(context.Background())

	mustMatch(t, reads.Load(), int64(1))
	mustMatch(t, client.State().Model, "")
}
