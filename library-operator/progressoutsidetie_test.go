package main

// What these tests prove. Two outside messages for one row can carry the
// same second: a reconcile dates each play at Jellyfin's last played time,
// so a second reconcile repeats the time of the first. At the same second
// the message wins only where it says more than the row: an ended play
// over one still running, and with the same ended state a larger
// position. The order is total and moves one way, so two publishers that
// disagree in one second settle on the message that says more and never
// overwrite each other again.

import "testing"

// One outside play for the tie tests, at the time the row holds.
func tiedOutside(position int, ended bool, person string) outsidePlay {
	return outsidePlay{Player: "jellyfin", People: []string{person},
		Position: position, Duration: 8160, Ended: ended, At: testOutsideAt}
}

func TestAnOutsidePlayAtTheSameSecondWinsWhenItSaysMore(t *testing.T) {
	cases := []struct {
		name     string
		row      outsidePlay
		message  outsidePlay
		position int
		ended    bool
		person   string
	}{
		{name: "the end of a played film over the start",
			row: tiedOutside(0, true, "person-a"), message: tiedOutside(8160, true, "person-b"),
			position: 8160, ended: true, person: "person-b"},
		{name: "an ended play over one still running",
			row: tiedOutside(4210, false, "person-a"), message: tiedOutside(4210, true, "person-b"),
			position: 4210, ended: true, person: "person-b"},
		{name: "a larger position over a smaller one",
			row: tiedOutside(4210, false, "person-a"), message: tiedOutside(4300, false, "person-b"),
			position: 4300, ended: false, person: "person-b"},
		{name: "a play still running under an ended one",
			row: tiedOutside(4210, true, "person-a"), message: tiedOutside(8160, false, "person-b"),
			position: 4210, ended: true, person: "person-a"},
		{name: "a smaller position under a larger one",
			row: tiedOutside(4210, true, "person-a"), message: tiedOutside(10, true, "person-b"),
			position: 4210, ended: true, person: "person-a"},
		{name: "the same position and ended state",
			row: tiedOutside(4210, false, "person-a"), message: tiedOutside(4210, false, "person-b"),
			position: 4210, ended: false, person: "person-a"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store, db := newSQLiteProgressStore(t)
			if err := store.recordOutside(t.Context(), "jellyfin-7-19", testCase.row); err != nil {
				t.Fatal(err)
			}

			if err := store.recordOutside(t.Context(), "jellyfin-7-19", testCase.message); err != nil {
				t.Fatal(err)
			}

			row := heldPlay(t, db, "jellyfin-7-19")
			if row.Position != testCase.position || (row.Ended != 0) != testCase.ended {
				t.Errorf("row = %+v, want position %d ended %t", row, testCase.position, testCase.ended)
			}
			if people := heldPeople(t, db, "jellyfin-7-19"); people[testCase.person] != 1 {
				t.Errorf("people = %v, want %s, which shows whose message the row holds", people, testCase.person)
			}
		})
	}
}
