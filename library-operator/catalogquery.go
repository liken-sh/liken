package main

// catalogquery.go is the read side of the catalog, over the agent's
// /v1/queries endpoint. The write client in catalog.go does not cover it.
// The prune reads the ids a walk did not mark through this side. The
// endpoint answers a query as a stream of newline-delimited JSON events,
// so the reader holds one row at a time and never the whole result set.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The queries endpoint every read posts to.
const queriesPath = "/v1/queries"

// queryReadLimit bounds the buffer the streaming reader grows for one
// event line, so a single event cannot grow the reader without end.
const queryReadLimit = 1 << 20

// LibraryKeys reads the sorted set of libraries this agent's own
// catalog holds rows for. It is the departure signal in plan 21: a
// survivor whose set no longer names a departed library has applied
// the deletes. The set needs no LIMIT, because it is bounded by the
// namespace's count of Libraries and not by its count of rows.
//
// A UNION rather than six reads, because UNION drops the duplicates,
// so one request answers the whole set, and every branch reads only
// the library-leading primary key its table already has.
func (c *Catalog) LibraryKeys(ctx context.Context) ([]string, error) {
	return c.queryStrings(ctx, libraryKeysSQL(), nil)
}

// libraryKeysSQL builds the read from the same table list the sweep
// deletes from, so a table added to the schema reaches both.
func libraryKeysSQL() string {
	branches := make([]string, len(catalogTables))
	for i, table := range catalogTables {
		branches[i] = `SELECT library FROM ` + table
	}
	return strings.Join(branches, " UNION ") + ` ORDER BY 1`
}

// queryStrings runs a read query and returns the first column of every
// row as a string. Every caller's query bounds its own answer, by a
// LIMIT or by a set that is small by nature, so the slice never holds
// a whole table.
func (c *Catalog) queryStrings(ctx context.Context, sql string, params []any) ([]string, error) {
	var out []string
	err := c.stream(ctx, sql, params, func(cells []any) error {
		if len(cells) == 0 {
			return nil
		}
		if value, ok := cells[0].(string); ok {
			out = append(out, value)
		}
		return nil
	})
	return out, err
}

// queryInt runs a read query and returns the first column of the first
// row as an integer, or zero where the query returned no row.
func (c *Catalog) queryInt(ctx context.Context, sql string, params []any) (int, error) {
	value := 0
	found := false
	err := c.stream(ctx, sql, params, func(cells []any) error {
		if found || len(cells) == 0 {
			return nil
		}
		// A SqliteValue integer arrives as a JSON number, which decodes to
		// float64, so the count reads back through float64.
		if number, ok := cells[0].(float64); ok {
			value = int(number)
			found = true
		}
		return nil
	})
	return value, err
}

// stream posts one read statement and calls onRow for every row event
// the agent streams back. It reads the body line by line, so it holds
// one event at a time and never buffers the whole result.
func (c *Catalog) stream(ctx context.Context, sql string, params []any, onRow func(cells []any) error) error {
	var statement any = sql
	if len(params) > 0 {
		statement = []any{sql, params}
	}
	payload, _ := json.Marshal(statement)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+queriesPath, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("catalog query: %s: %s", resp.Status, message)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), queryReadLimit)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		cells, isError, message, err := decodeQueryEvent(line)
		if err != nil {
			return err
		}
		if isError {
			return fmt.Errorf("catalog query: %s", message)
		}
		if cells == nil {
			continue
		}
		if err := onRow(cells); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// Reads how many titles the catalog holds for this library: the movie
// rows, the series rows, and the franchise rows, which are the folders a
// walk reads, and never the episodes under a series.
func (c *Catalog) countTitles(ctx context.Context, library string) (int, error) {
	return c.queryInt(ctx, `SELECT `+
		`(SELECT count(*) FROM movies WHERE library = ?) + `+
		`(SELECT count(*) FROM series WHERE library = ?) + `+
		`(SELECT count(*) FROM franchises WHERE library = ?)`,
		[]any{library, library, library})
}

// The subscriptions endpoint, which answers one statement with the
// rows it holds now and then every later change to them.
const subscriptionsPath = "/v1/subscriptions"

// Posts one statement and calls onRow for every row of the opening
// snapshot and every change after it, with the column names the stream
// opened with, because the agent's matcher prepends the primary key to
// the projection and a reader that counts cells would read the wrong one.
// onReady is called once the snapshot ends. The call returns when the
// stream ends, which a cancelled context is one way to do.
func (c *Catalog) subscribe(ctx context.Context, sql string, onReady func(), onRow func(columns []string, cells []any)) error {
	payload, _ := json.Marshal(sql)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+subscriptionsPath, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("catalog subscription: %s: %s", resp.Status, message)
	}

	var columns []string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), queryReadLimit)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		event, err := decodeSubscriptionEvent(line)
		if err != nil {
			return err
		}
		switch event.kind {
		case subscriptionColumns:
			columns = event.columns
		case subscriptionRow:
			onRow(columns, event.cells)
		case subscriptionEnd:
			onReady()
		case subscriptionError:
			return fmt.Errorf("catalog subscription: %s", event.message)
		}
	}
	return scanner.Err()
}

// The four events a subscription stream carries that the reader
// acts on; every other event reads as a skipped one.
const (
	subscriptionColumns = "columns"
	subscriptionRow     = "row"
	subscriptionEnd     = "eoq"
	subscriptionError   = "error"
)

// One decoded event of a subscription stream.
type subscriptionEvent struct {
	kind    string
	columns []string
	cells   []any
	message string
}

// Reads one streamed subscription event. A row event is
// [rowid, [cells]] and a change event is [kind, rowid, [cells], id], so
// both carry their cells and both read as a row here.
func decodeSubscriptionEvent(line []byte) (subscriptionEvent, error) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(line, &event); err != nil {
		return subscriptionEvent{}, err
	}
	if raw, held := event[subscriptionError]; held {
		var message string
		_ = json.Unmarshal(raw, &message)
		return subscriptionEvent{kind: subscriptionError, message: message}, nil
	}
	if raw, held := event[subscriptionColumns]; held {
		var columns []string
		if err := json.Unmarshal(raw, &columns); err != nil {
			return subscriptionEvent{}, err
		}
		return subscriptionEvent{kind: subscriptionColumns, columns: columns}, nil
	}
	if _, held := event[subscriptionEnd]; held {
		return subscriptionEvent{kind: subscriptionEnd}, nil
	}
	raw, held := event[subscriptionRow]
	at := 1
	if !held {
		raw, held = event["change"]
		at = 2
	}
	if !held {
		return subscriptionEvent{}, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return subscriptionEvent{}, err
	}
	if len(parts) <= at {
		return subscriptionEvent{}, nil
	}
	var cells []any
	if err := json.Unmarshal(parts[at], &cells); err != nil {
		return subscriptionEvent{}, err
	}
	return subscriptionEvent{kind: subscriptionRow, cells: cells}, nil
}

// Reads one named column out of a streamed row.
func cellNamed(columns []string, cells []any, name string) (any, bool) {
	for at, column := range columns {
		if column == name && at < len(cells) {
			return cells[at], true
		}
	}
	return nil, false
}

// decodeQueryEvent reads one streamed query event. A row event carries
// the row's cells, an error event carries a message, and the columns and
// end-of-query events carry neither, so they read as a skipped event.
func decodeQueryEvent(line []byte) (cells []any, isError bool, message string, err error) {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(line, &event); err != nil {
		return nil, false, "", err
	}
	if raw, ok := event["error"]; ok {
		_ = json.Unmarshal(raw, &message)
		return nil, true, message, nil
	}
	raw, ok := event["row"]
	if !ok {
		return nil, false, "", nil
	}
	// A row event is [rowid, [cells...]], so the cells are the second
	// element of the pair.
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil {
		return nil, false, "", err
	}
	if len(pair) != 2 {
		return nil, false, "", nil
	}
	if err := json.Unmarshal(pair[1], &cells); err != nil {
		return nil, false, "", err
	}
	return cells, false, "", nil
}
