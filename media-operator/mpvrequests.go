package main

// This file logs each mpv command that a person caused, together with
// mpv's reply. mpv replies to a command that has a request id with the
// same id and an error string, which is "success" when the command ran.
// The command sidecar gives a request id to each command that a key
// press or a published command sends, and keeps the log line until the
// reply arrives. So one line shows what caused the command, the JSON
// that the sidecar sent, and mpv's reply.
//
// Commands that the sidecar sends on its own, such as the observe
// requests or the repeat of a held key, have no request id. mpv replies
// to those with request id 0, and answer drops the reply because no
// request uses id 0.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// mpvReplySuccess is the error string of mpv's reply to a command that
// ran.
const mpvReplySuccess = "success"

// mpvReply is mpv's reply to a command that had a request id. Data is
// the JSON text of the value that a get_property returns.
type mpvReply struct {
	ID    int
	Error string
	Data  string
}

// mpvRequest is a log line that waits for mpv's replies. One line can
// cover several commands, such as the level and the mute of one volume
// change, so left counts the replies that have not arrived, and the
// line is logged when left reaches 0. results has each distinct error
// string of the replies.
type mpvRequest struct {
	line    string
	left    int
	results []string
}

// commandJSON returns a command as the JSON that goes over mpv's
// socket, so the log line shows exactly what the sidecar sent.
func commandJSON(command []any) string {
	// Marshaling strings and numbers cannot fail.
	words, _ := json.Marshal(command)
	return string(words)
}

// request sends commands to mpv as one operation and logs line when
// mpv has replied to all of them. When the socket is not open or a
// write fails, it logs the line at once with the reason, because no
// reply will arrive.
func (c *commander) request(line string, commands ...[]any) {
	c.mpvMutex.Lock()
	defer c.mpvMutex.Unlock()
	sent := make([]string, len(commands))
	for index, command := range commands {
		sent[index] = commandJSON(command)
	}
	line += ", sent " + strings.Join(sent, " and ") + " to mpv"
	if c.mpv == nil {
		logLine(c.log, "%s, not delivered, because mpv's socket is not open", line)
		return
	}
	entry := &mpvRequest{line: line, left: len(commands)}
	for _, command := range commands {
		c.requestSeq++
		id := c.requestSeq
		if c.requests == nil {
			c.requests = map[int]*mpvRequest{}
		}
		c.requests[id] = entry
		if err := sendRequest(c.mpv, id, command); err != nil {
			delete(c.requests, id)
			logLine(c.log, "%s, not delivered: %v", line, err)
			return
		}
	}
}

// deliverReply matches a reply from mpv to the query or the log line that
// waits for its id, and logs the line when its last reply arrives. It
// drops a reply whose id nothing waits for.
func (c *commander) deliverReply(reply mpvReply) {
	c.mpvMutex.Lock()
	if query, reading := c.queries[reply.ID]; reading {
		delete(c.queries, reply.ID)
		c.mpvMutex.Unlock()
		query <- reply
		return
	}
	entry, waiting := c.requests[reply.ID]
	if !waiting {
		c.mpvMutex.Unlock()
		return
	}
	delete(c.requests, reply.ID)
	entry.left--
	if !slices.Contains(entry.results, reply.Error) {
		entry.results = append(entry.results, reply.Error)
	}
	c.mpvMutex.Unlock()
	if entry.left == 0 {
		logLine(c.log, "%s, mpv answered %s", entry.line, strings.Join(entry.results, " and "))
	}
}

// serveReplies passes each reply from mpv to answer until the replies
// channel closes.
func (c *commander) serveReplies(replies <-chan mpvReply) {
	for reply := range replies {
		c.deliverReply(reply)
	}
}

// abandonRequests logs every line still waiting when the socket closes,
// because mpv can no longer reply. The caller holds mpvMutex.
func (c *commander) abandonRequests() {
	logged := map[*mpvRequest]bool{}
	ids := make([]int, 0, len(c.requests))
	for id := range c.requests {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		entry := c.requests[id]
		if !logged[entry] {
			logged[entry] = true
			logLine(c.log, "%s, mpv closed its socket before it answered", entry.line)
		}
	}
	c.requests = nil
}

// abandonQueries closes the channel of every query still waiting when
// the socket closes, so each query returns an error. The caller holds
// mpvMutex.
func (c *commander) abandonQueries() {
	for id, query := range c.queries {
		close(query)
		delete(c.queries, id)
	}
}

// mpvQueryTimeout bounds the wait for mpv's reply to a query. mpv
// replies to get_property at once, so a query that waits this long
// means that mpv has stopped reading its socket.
const mpvQueryTimeout = 5 * time.Second

// query reads one property from mpv and returns its value as JSON text.
// serveReplies delivers the reply on its own goroutine, so the caller
// must not be that goroutine. When mpv replies with an error,
// the returned error includes mpv's error string.
func (c *commander) query(property string) (string, error) {
	c.mpvMutex.Lock()
	if c.mpv == nil {
		c.mpvMutex.Unlock()
		return "", errors.New("mpv's socket is not open")
	}
	c.requestSeq++
	id := c.requestSeq
	answer := make(chan mpvReply, 1)
	if c.queries == nil {
		c.queries = map[int]chan mpvReply{}
	}
	c.queries[id] = answer
	err := sendRequest(c.mpv, id, []any{"get_property", property})
	if err != nil {
		delete(c.queries, id)
	}
	c.mpvMutex.Unlock()
	if err != nil {
		return "", err
	}
	select {
	case reply, answered := <-answer:
		switch {
		case !answered:
			return "", errors.New("mpv closed its socket before it answered")
		case reply.Error != mpvReplySuccess:
			return "", fmt.Errorf("mpv answered %s", reply.Error)
		}
		return reply.Data, nil
	case <-time.After(mpvQueryTimeout):
		c.mpvMutex.Lock()
		delete(c.queries, id)
		c.mpvMutex.Unlock()
		return "", fmt.Errorf("mpv did not answer in %s", mpvQueryTimeout)
	}
}

// sendRequest writes one command as JSON, with the request id that mpv
// copies into its reply.
func sendRequest(writer io.Writer, id int, command []any) error {
	return json.NewEncoder(writer).Encode(mpvCommand{Command: command, RequestID: id})
}
