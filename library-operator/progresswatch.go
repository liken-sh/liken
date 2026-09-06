package main

// A Watch is a set of people on one item, and its progress is the
// latest Play recorded against it. The role publishes that projection
// after every write to a Play in a Watch, and the operator writes it
// into the Watch's status.
//
// The positions travel the bus as H:MM:SS and the store holds seconds,
// so the two conversions are here, beside the one reader that needs
// both.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultProgressClient is the HTTP client the role reaches its own
// agent with. It states no timeout, because every request bounds itself
// with a context.
func defaultProgressClient() *http.Client {
	return &http.Client{}
}

// publishWatch republishes the Watch that one Play belongs to. A Play
// in no Watch publishes nothing, and a read that fails leaves the
// retained projection where it is, so a store that stops answering
// never reads as a Watch at the beginning.
func (p *progress) publishWatch(ctx context.Context, play string) {
	watch, err := p.store.watchOf(ctx, play)
	if err != nil {
		p.logf("could not read the watch of %s: %v", play, err)
		return
	}
	if watch == "" {
		return
	}
	row, held, err := p.store.latestForWatch(ctx, watch)
	if err != nil {
		p.logf("could not read the latest play of %s: %v", watch, err)
		return
	}
	if !held {
		return
	}
	payload, _ := json.Marshal(watchProgress{
		Play:         row.Play,
		Item:         row.Item,
		Position:     formatPosition(row.Position),
		Duration:     formatPosition(row.Duration),
		Season:       row.Season,
		Episode:      row.Episode,
		Ended:        row.Ended != 0,
		LastRecorded: time.Unix(row.Recorded, 0).UTC().Format(time.RFC3339),
	})
	p.bus.Publish(watchProgressTopic(p.topicBase, p.namespace, watch), payload, true)
}

// parsePosition reads H:MM:SS as seconds. The hours may run past one
// digit, because a Play of a list runs as long as the list does. An
// empty value is the start of a Play and reads as zero; any other shape
// answers ok false, and the caller records zero and logs it.
func parsePosition(value string) (int, bool) {
	if value == "" {
		return 0, true
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, false
	}
	seconds := 0
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return 0, false
		}
		seconds = seconds*60 + number
	}
	return seconds, true
}

// formatPosition writes seconds back as H:MM:SS, the shape every
// position on the bus carries.
func formatPosition(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}
