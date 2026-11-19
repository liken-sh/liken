package main

// The channel poller fetches channel.yaml from the Cluster's release
// source, so the Cluster's status can show the newest release on the
// channel. The version is for display only, because a
// machine installs a release only from the digest-pinned entry in the
// Cluster's catalog, never from the channel document. So a stale or
// missing version does no harm, and the poller
// keeps the last version when a fetch fails.
//
// A pass runs every ten seconds, and a channel changes only when
// someone publishes a release, so the poller fetches once per
// channelPollInterval. It fetches at once when the release source
// changes, or when the Cluster's liken.sh/check-releases annotation
// takes a new value.
//
// The fetch runs on its own goroutine, so a slow or unreachable release
// server does not delay the rest of the pass, such as marking a
// machine Lost.

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// channelPollInterval is the time from the start of one fetch to the
// next. People publish releases, so six hours is soon enough to show
// that an upgrade exists. A person who wants to see a new release at
// once changes the check-releases annotation.
const channelPollInterval = 6 * time.Hour

type channelPoller struct {
	mu        sync.Mutex
	source    string    // the release source that the poller follows
	check     string    // the last check-releases annotation value seen
	polled    time.Time // when the last fetch started
	inFlight  bool
	available string // the latest version from the last successful fetch

	fetch func(url string) ([]byte, error)
}

func newChannelPoller() *channelPoller {
	return &channelPoller{fetch: fetchChannelDocument}
}

// Observe is called by each pass with the spec's releases section and
// the Cluster's check-releases annotation. It starts a fetch in the
// background when one is due and none is running, and it never blocks.
func (p *channelPoller) Observe(releases cluster.ClusterReleasesSpec, check string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Without a source there is nothing to fetch, and the version from
	// a previous source no longer applies.
	if releases.Source == "" {
		p.source, p.check, p.available = "", "", ""
		return
	}

	if releases.Source != p.source {
		// The old source's version does not describe the new channel.
		p.source, p.check, p.available = releases.Source, check, ""
		p.polled = time.Time{}
	} else if check != p.check {
		// The last version stays available until the fetch replaces
		// it.
		p.check = check
		p.polled = time.Time{}
	}

	if p.inFlight || now.Sub(p.polled) < channelPollInterval {
		return
	}
	// The time is recorded when the fetch starts, not when it
	// succeeds, so an unreachable server is retried once per interval
	// and not on every pass.
	p.polled = now
	p.inFlight = true
	go p.poll(p.source)
}

// Available returns the latest version from the last successful
// fetch, or "" before one succeeds.
func (p *channelPoller) Available() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.available
}

// poll fetches and parses the channel document, and stores the latest
// version unless the source changed while the fetch ran.
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
		fmt.Printf("fetching the channel document %s: %v\n", url, err)
		return
	}
	p.available = latest
}

func fetchChannelDocument(url string) ([]byte, error) {
	return fetchChannelDocumentVia(nil, url)
}

// fetchChannelDocumentVia fetches url with transport, or with the
// default transport when transport is nil. The document is a few lines
// of YAML, so it reads at most 1 MiB. The 30-second timeout keeps a
// server that never responds from leaving inFlight set forever.
func fetchChannelDocumentVia(transport http.RoundTripper, url string) ([]byte, error) {
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
