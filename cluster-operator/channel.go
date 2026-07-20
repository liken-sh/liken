package main

// This poller checks the release channel. It shows how a cluster
// learns that a new version exists.
//
// The channel's root document, channel.yaml, names the latest
// published version. This poller fetches that document so the sweep
// can show the answer in the Cluster's status.
//
// Two facts shape this design. First, the answer is advisory. The
// machine package's channel.go explains why the design keeps the
// answer outside the trust chain. Because the answer is advisory, a
// stale or missing answer costs nothing. The poller keeps the last
// answer it received and tries again later. Second, the sweep runs
// every ten seconds, but the channel changes about once a week. So
// the poller fetches the document on a long interval instead of every
// sweep. Two things make the poller fetch immediately: a new source,
// or a new value in spec.releases.check. The check field is a
// declared request to poll immediately.
//
// The fetch runs on its own goroutine. The machine operator's release
// fetcher uses the same method. The sweep must keep judging the fleet
// at its own speed, and a slow or dead release server must never
// delay a Lost verdict. The Observe function decides what to do and
// returns immediately; the goroutine reports its result back through
// the mutex.

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// channelPollInterval sets how long an answer stays fresh, so the
// poller does not ask again during this time. People publish releases
// on a human timescale, and the poll answers the question "should
// someone think about upgrading?", not a real-time telemetry
// question. A person who just published a release and wants the
// answer now can edit spec.releases.check to request an immediate
// poll.
const channelPollInterval = 6 * time.Hour

type channelPoller struct {
	mu        sync.Mutex
	source    string    // the channel this poller last asked
	check     string    // the last check value this poller handled
	polled    time.Time // when the last attempt started
	inFlight  bool
	available string // the channel's last known answer

	// fetch performs the actual HTTP request. Tests can replace it
	// with a fake function, so they do not need a real channel
	// server.
	fetch func(url string) ([]byte, error)
}

func newChannelPoller() *channelPoller {
	return &channelPoller{fetch: fetchChannelDocument}
}

// Observe runs once per sweep. It receives the spec's current
// releases section. It decides whether a poll is due: the source or
// the check value changed, or the last answer is too old. If a poll
// is due, Observe starts it in the background. Observe never blocks.
func (p *channelPoller) Observe(releases cluster.ClusterReleasesSpec, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If there is no source, there is nothing to poll. A cluster
	// without a channel has no available version, and it does not
	// keep an available version from a channel it had before.
	if releases.Source == "" {
		p.source, p.check, p.available = "", "", ""
		return
	}

	if releases.Source != p.source {
		// A different channel's answer does not apply here. The
		// poller drops it and asks the new channel immediately.
		p.source, p.check, p.available = releases.Source, releases.Check, ""
		p.polled = time.Time{}
	} else if releases.Check != p.check {
		// This is the request to poll immediately. The poller honors
		// it once, by marking the last answer as stale right away.
		// The last answer stays visible until the fresh answer
		// replaces it.
		p.check = releases.Check
		p.polled = time.Time{}
	}

	if p.inFlight || now.Sub(p.polled) < channelPollInterval {
		return
	}
	// The poller records the time of this attempt before it runs.
	// This way, a dead server is asked again on the interval, not on
	// every ten-second sweep.
	p.polled = now
	p.inFlight = true
	go p.poll(p.source)
}

// Available returns the latest version the channel last announced.
// It is empty until a poll succeeds.
func (p *channelPoller) Available() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.available
}

// poll fetches and parses the channel document. It keeps the answer
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
		// The last answer stands. Advisory data may be stale. The
		// interval, or an edit to the check value, will trigger
		// another poll.
		fmt.Printf("polling the release channel %s: %v\n", url, err)
		return
	}
	p.available = latest
}

// fetchChannelDocument sends a GET request for the channel document,
// using a client with a bounded size and time limit. The document is
// only a few lines of YAML, so the size limit is generous. The time
// limit stops a stuck server from keeping the poll in progress
// forever.
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
