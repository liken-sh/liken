package main

// Polling the release channel: how a cluster learns that something
// newer exists.
//
// The channel's root document (channel.yaml) names the latest
// published version, and this poller fetches it so the sweep can
// surface that answer in the Cluster's status. Two facts shape the
// design. First, the answer is advisory (the machine package's
// channel.go explains why it is deliberately outside the trust
// chain), so a stale or missing answer costs nothing — the poller
// keeps the last one it saw and tries again later. Second, the sweep
// runs every ten seconds against a channel that changes maybe weekly,
// so the poller is lazy: it fetches on a long interval, and only the
// spec changing underneath it (a new source, or a new
// spec.releases.check value — the declared "poll now" nudge) makes it
// go immediately.
//
// The fetch itself runs on its own goroutine, the same posture as the
// machine operator's release fetcher: the sweep must keep judging the
// fleet at its own cadence, and a slow or dead release server must
// never stall a Lost verdict. Observe decides and returns; the
// goroutine reports back through the mutex.

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/machine"
)

// channelPollInterval is how long an answer is fresh enough not to
// re-ask. Releases are cut on human timescales, and the point of the
// poll is "should someone think about upgrading?", not real-time
// telemetry; anyone who just published and wants the answer now edits
// spec.releases.check.
const channelPollInterval = 6 * time.Hour

type channelPoller struct {
	mu        sync.Mutex
	source    string    // the channel last asked about
	check     string    // the spec's check value last honored
	polled    time.Time // when the last attempt started
	inFlight  bool
	available string // the channel's answer, as last seen

	// fetch is the one impure edge, injectable so tests can stand in
	// a channel without a server.
	fetch func(url string) ([]byte, error)
}

func newChannelPoller() *channelPoller {
	return &channelPoller{fetch: fetchChannelDocument}
}

// Observe is called once per sweep with the spec's current releases
// stanza. It decides whether a poll is due — the source or check
// changed, or the last answer has aged out — and starts one in the
// background if so. It never blocks.
func (p *channelPoller) Observe(releases machine.ClusterReleasesSpec, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// No source, nothing to poll: a cluster without a channel has no
	// "available" and keeps none from a channel it used to have.
	if releases.Source == "" {
		p.source, p.check, p.available = "", "", ""
		return
	}

	if releases.Source != p.source {
		// A different channel's answer is meaningless here; drop it
		// and ask the new channel immediately.
		p.source, p.check, p.available = releases.Source, releases.Check, ""
		p.polled = time.Time{}
	} else if releases.Check != p.check {
		// The declared nudge: honor it once, by making the answer
		// instantly stale. The last answer stays visible until the
		// fresh one replaces it.
		p.check = releases.Check
		p.polled = time.Time{}
	}

	if p.inFlight || now.Sub(p.polled) < channelPollInterval {
		return
	}
	// Stamp the attempt before it runs so a dead server is re-asked
	// on the interval, not on every ten-second sweep.
	p.polled = now
	p.inFlight = true
	go p.poll(p.source)
}

// Available is the latest version the channel last announced, empty
// until a poll has succeeded.
func (p *channelPoller) Available() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.available
}

// poll fetches and parses the channel document, and keeps the answer
// only if the sweep is still asking about the same source.
func (p *channelPoller) poll(source string) {
	url := strings.TrimSuffix(source, "/") + "/channel.yaml"
	raw, err := p.fetch(url)
	var latest string
	if err == nil {
		var channel *machine.Channel
		if channel, err = machine.ParseChannel(raw); err == nil {
			latest = channel.Latest
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight = false
	if source != p.source {
		return
	}
	if err != nil {
		// The last answer stands: advisory data is allowed to be
		// stale, and the interval (or a check edit) will re-ask.
		fmt.Printf("polling the release channel %s: %v\n", url, err)
		return
	}
	p.available = latest
}

// fetchChannelDocument GETs the channel document with a bounded
// client. The document is a few lines of YAML; the size cap is
// generosity, and the timeout is what keeps a wedged server from
// pinning the poll in flight forever.
func fetchChannelDocument(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
