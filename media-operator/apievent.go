package main

// This file writes the record a capture leaves on the cluster. Every
// request that produces bytes posts a Captured Event on the Player, so
// `kubectl describe player` answers who looked and when without a log
// search. The Event names the caller, the aspect, and the media type,
// and nothing else: the request log line is the detail record, with
// the request id. Neither of them ever holds the token or any hash of
// it, because anyone with get on events in the namespace reads the
// Event.
//
// The message holds no request id, so the same caller who takes the
// same capture again within ten minutes adds to the count of one
// Event, and `kubectl describe` prints one line for the repeats. The
// log lines of that caller, at the times of the Event, give each
// request.

// recordCapture posts the Captured Event for one request. The recorder
// queues it and returns, so a slow or refused write never delays or
// fails the capture: the record is secondary to the bytes, and the
// client already holds them.
func (s *apiServer) recordCapture(e *apiExchange, player *Player, aspect, mediaType string) {
	s.recorder.Normal(playerRef(player), reasonCaptured,
		e.subject+" took the "+aspect+" of "+player.Metadata.Name+" as "+mediaType)
}
