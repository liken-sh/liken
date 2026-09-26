package main

// The command sidecar's record of the commands a person caused. mpv
// answers every command on its socket with the request id the command
// carried and one error word, "success" for a command it ran. The
// sidecar gives a request id to each command a press or a published
// command sends, holds the line that describes it, and writes the line
// when mpv's answer arrives. So one line says what triggered the
// command, what the sidecar sent, and what mpv answered.
//
// The commands the sidecar sends for its own reasons, such as a
// presentation block, the observe requests, or a repeat of a held key,
// carry no request id. mpv answers those with id 0, and the sidecar
// reads nothing from that answer.

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
)

// mpvAnswerSuccess is the error word mpv writes for a command it ran.
const mpvAnswerSuccess = "success"

// mpvReply is one answer mpv wrote to a command that carried a request
// id.
type mpvReply struct {
	ID    int
	Error string
}

// mpvRequest is one line that waits for mpv's answers. A line can cover
// more than one command, such as the level and the mute of one volume
// state, so it waits until every command it sent has an answer.
type mpvRequest struct {
	line    string
	left    int
	answers []string
}

// mpvWords writes a command the way mpv's socket carries it, so the line
// shows the exact words the sidecar sent.
func mpvWords(command []any) string {
	// A command is strings and numbers, and those marshal
	// unconditionally, so the error is dropped.
	words, _ := json.Marshal(command)
	return string(words)
}

// request sends commands to mpv as one operation and logs line when
// mpv has answered all of them. A socket that is not open, and a write
// that fails, log the line at once with the reason, because no answer
// will arrive.
func (c *commander) request(line string, commands ...[]any) {
	c.mpvMutex.Lock()
	defer c.mpvMutex.Unlock()
	sent := make([]string, len(commands))
	for index, command := range commands {
		sent[index] = mpvWords(command)
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

// answer folds one of mpv's answers into the line that waits for it,
// and logs the line when its last answer arrives. An id the sidecar did
// not give, or one it already answered, is dropped.
func (c *commander) answer(reply mpvReply) {
	c.mpvMutex.Lock()
	entry, waiting := c.requests[reply.ID]
	if !waiting {
		c.mpvMutex.Unlock()
		return
	}
	delete(c.requests, reply.ID)
	entry.left--
	if !slices.Contains(entry.answers, reply.Error) {
		entry.answers = append(entry.answers, reply.Error)
	}
	c.mpvMutex.Unlock()
	if entry.left == 0 {
		logLine(c.log, "%s, mpv answered %s", entry.line, strings.Join(entry.answers, " and "))
	}
}

// serveReplies answers each of mpv's replies until the socket closes.
func (c *commander) serveReplies(replies <-chan mpvReply) {
	for reply := range replies {
		c.answer(reply)
	}
}

// abandonRequests logs every line still waiting when the socket closes,
// because mpv will not answer it now. The caller holds mpvMutex.
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

// sendRequest writes one command with the request id mpv answers under.
func sendRequest(writer io.Writer, id int, command []any) error {
	return json.NewEncoder(writer).Encode(mpvCommand{Command: command, RequestID: id})
}
