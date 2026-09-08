package main

// The bridge's half of the one-tile-in-flight design. A held scrub can
// broadcast tile requests faster than the bridge crops them, and every reply
// redraws the tile, so a queue of stale requests keeps the tile moving after
// the hand lifts. The message loop therefore drains what is queued and serves
// only the newest tile request. The display is the first guard: it sends one
// request per round trip. This is the second, for a burst from an older
// display, or for the round trip itself. The exit press, the presentation
// request, and art of every other kind are still served in order.

// isTrickplayRequest names the one kind of art request the bridge drops an
// older copy of. A logo and a cover are asked for once per item and size, so
// they never queue.
func isTrickplayRequest(args []string) bool {
	request, ok := parseArtRequest(args)
	return ok && request.kind == artKindTrickplay
}

// drainTrickplay reads what is already queued and never waits: the default
// case ends the drain at the first empty read, and a closed channel ends it
// as well. It returns the newest trickplay request among the ones it read, and
// every other message in the order it read them. The caller serves those and
// drops the trickplay requests they overtook.
func drainTrickplay(latest []string, messages <-chan clientMessage) (last []string, others [][]string) {
	for {
		select {
		case message, open := <-messages:
			if !open {
				return latest, others
			}
			if isTrickplayRequest(message.Args) {
				latest = message.Args
				continue
			}
			others = append(others, message.Args)
		default:
			return latest, others
		}
	}
}
