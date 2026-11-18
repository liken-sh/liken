package main

// recordCapture posts a Captured Event on the Player for each capture,
// so `kubectl describe player` shows who captured what and when. The
// message has no request id, so a caller that repeats the same capture
// within ten minutes increases the count of one Event, and the request
// log lines list each request. Neither the Event nor the log line
// contains the token or a hash of it, because anyone who can read Events
// in the namespace can read them. The recorder queues the Event and
// returns, so a slow or refused write never delays or fails the capture.
func (s *apiServer) recordCapture(e *apiExchange, player *Player, aspect, mediaType string) {
	s.recorder.Normal(playerRef(player), reasonCaptured,
		e.subject+" took the "+aspect+" of "+player.Metadata.Name+" as "+mediaType)
}
