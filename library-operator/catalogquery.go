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
)

// The queries endpoint every read posts to.
const queriesPath = "/v1/queries"

// queryReadLimit bounds the buffer the streaming reader grows for one
// event line, so a single event cannot grow the reader without end.
const queryReadLimit = 1 << 20

// queryStrings runs a read query and returns the first column of every
// row as a string. The query carries its own LIMIT, so the slice holds
// one bounded batch and never the whole table.
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
