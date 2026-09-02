package main

// The per-table update stream of the agent's loopback API. An event
// says that a row of the table changed, and nothing more. That is
// enough for the reporter, which reads the table's counts again on
// any change and needs no values from the stream.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
