package main

// What these tests prove. How little of a work must be left before a play
// of it counts as watched, at the boundary second and the second before
// it.

import "testing"

func TestWhenAWorkCountsAsWatched(t *testing.T) {
	for _, test := range []struct {
		name     string
		position int
		duration int
		watched  bool
	}{
		{name: "a 22 minute sitcom with 1m06s left", position: 1320 - 66, duration: 1320, watched: true},
		{name: "a 22 minute sitcom one second before that", position: 1320 - 67, duration: 1320},
		{name: "a 42 minute drama with 2m06s left", position: 2520 - 126, duration: 2520, watched: true},
		{name: "a 42 minute drama one second before that", position: 2520 - 127, duration: 2520},
		{name: "a 100 minute film with 5m00s left", position: 6000 - 300, duration: 6000, watched: true},
		{name: "a 100 minute film one second before that", position: 6000 - 301, duration: 6000},
		{name: "a 150 minute film with 5m00s left", position: 9000 - 300, duration: 9000, watched: true},
		{name: "a 150 minute film one second before that", position: 9000 - 301, duration: 9000},
		{name: "a film played to the end", position: 6000, duration: 6000, watched: true},
		{name: "a film played past its stated end", position: 6100, duration: 6000, watched: true},
		{name: "a film at the start", position: 0, duration: 6000},
		{name: "a play that carried no duration", position: 0, duration: 0},
		{name: "a position in a play that carried no duration", position: 600, duration: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := watched(test.position, test.duration); got != test.watched {
				t.Errorf("watched = %v, want %v", got, test.watched)
			}
		})
	}
}
