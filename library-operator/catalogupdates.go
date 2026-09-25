package main

// The per-table update stream of the agent's loopback API. An event
// says that a row of the table changed, and nothing more. That is
// enough for the reporter, which reads the table's counts again on
// any change, and for a phase of a library Job, which reads its gap
// again. Neither needs values from the stream.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The update endpoint, one path per replicated table.
const updatesPath = "/v1/updates/"

// The event a table's update stream sends for every row that changed.
// An insert arrives as an update, and a delete as a delete.
const updateNotify = "notify"

// Follows one table's update stream until it ends. onOpen is
// called once the agent accepts the stream, and onChange for every row
// event, which carries the row's primary key and never its library.
func (c *Catalog) followUpdates(ctx context.Context, table string, onOpen func(), onChange func()) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+updatesPath+table, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("catalog updates of %s: %s: %s", table, resp.Status, message)
	}
	onOpen()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), queryReadLimit)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		if raw, held := event[subscriptionError]; held {
			var message string
			_ = json.Unmarshal(raw, &message)
			return fmt.Errorf("catalog updates of %s: %s", table, message)
		}
		if _, held := event[updateNotify]; held {
			onChange()
		}
	}
	return scanner.Err()
}

// Follows one table's update stream and marks a change on every
// event, opening the stream again after a backoff for as long as the
// context runs. The stream's events name no library, so the mark says
// only that something moved.
func followTableChanges(ctx context.Context, catalog *Catalog, table string, changed chan<- struct{},
	logf func(format string, args ...any)) {
	backoff := reportMinBackoff
	for ctx.Err() == nil {
		opened := false
		err := catalog.followUpdates(ctx, table,
			func() { opened = true },
			func() { markChanged(changed) })
		if err != nil && ctx.Err() == nil {
			logf("the update stream of %s ended: %v", table, err)
		}
		// The events between one stream and the next are gone, so
		// a stream that ended marks a change and the reader reads what
		// the catalog holds now.
		markChanged(changed)
		if opened {
			backoff = reportMinBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !opened {
			backoff = min(backoff*2, reportMaxBackoff)
		}
	}
}

// The same agent through a client with no timeout, because a stream stays
// open for the life of the container and a client timeout would cut it. The
// transport is the catalog's own, so a test's server answers both.
func (c *Catalog) streaming() *Catalog {
	return &Catalog{base: c.base, http: &http.Client{Transport: c.http.Transport}}
}
