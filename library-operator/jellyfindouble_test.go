package main

// What these tests prove: Jellyfin holds a file of two episodes as one
// item that names the first episode and the last, and the role finds that
// item for either episode, so a Play of the second episode writes its file
// position to the item.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// A listing answer in the shape Jellyfin gives for a double: the first
// episode in IndexNumber, the last in IndexNumberEnd, and the run time of
// the whole file. The role asks for no extra field to get IndexNumberEnd,
// because Jellyfin returns it with every item that carries it.
const jellyfinDoubleListing = `{"Items":[{"Name":"The Long Way Down","Id":"item-double",` +
	`"RunTimeTicks":26400000000,"IndexNumber":10,"IndexNumberEnd":11,"ParentIndexNumber":4,` +
	`"IsFolder":false,"Type":"Episode","SeriesId":"item-series","ProviderIds":{"Tvdb":"9001"},` +
	`"LocationType":"FileSystem","MediaType":"Video"}],"TotalRecordCount":1,"StartIndex":0}`

func TestAListingDecodesTheLastEpisodeOfADouble(t *testing.T) {
	list := jellyfinItemList{}
	if err := json.Unmarshal([]byte(jellyfinDoubleListing), &list); err != nil {
		t.Fatal(err)
	}

	want := jellyfinItem{ID: "item-double", Type: "Episode", SeriesID: "item-series",
		ProviderIds: map[string]string{"Tvdb": "9001"}, ParentIndexNumber: 4, IndexNumber: 10,
		IndexNumberEnd: 11, RunTimeTicks: 26400000000}
	if len(list.Items) != 1 || !reflect.DeepEqual(list.Items[0], want) {
		t.Errorf("items = %+v, want %+v", list.Items, want)
	}
}

// The fixture server with three files of several episodes in the series:
// a double in season 1, a double whose server recorded no last episode in
// season 2, and an item in season 4 whose range is longer than the scanner
// reads as one file.
func jellyfinDoubleFixture() *fakeJellyfin {
	fake := jellyfinFixture()
	fake.items = append(fake.items,
		jellyfinItem{ID: "item-double", Type: "Episode", SeriesID: "item-series",
			ParentIndexNumber: 1, IndexNumber: 1, IndexNumberEnd: 2, RunTimeTicks: 26_400_000_000},
		jellyfinItem{ID: "item-unmarked-double", Type: "Episode", SeriesID: "item-series",
			ParentIndexNumber: 2, IndexNumber: 1, RunTimeTicks: 26_400_000_000},
		jellyfinItem{ID: "item-long-range", Type: "Episode", SeriesID: "item-series",
			ParentIndexNumber: 4, IndexNumber: 1, IndexNumberEnd: 9, RunTimeTicks: 26_400_000_000},
	)
	return fake
}

// The double answers for each episode it holds. An item with no last
// episode answers only its first. A range longer than maxRangeEpisodes
// answers only its first episode too, because the scanner reads such a
// name as one episode, and the two must agree on which episodes share
// the file.
func TestTheIndexFindsADoubleForEachOfItsEpisodes(t *testing.T) {
	for _, test := range []struct {
		name    string
		season  int
		episode int
		item    string
	}{
		{name: "the first episode of a double", season: 1, episode: 1, item: "item-double"},
		{name: "the second episode of a double", season: 1, episode: 2, item: "item-double"},
		{name: "past the end of a double", season: 1, episode: 3, item: ""},
		{name: "the first episode of an item with no last episode", season: 2, episode: 1, item: "item-unmarked-double"},
		{name: "the second episode of an item with no last episode", season: 2, episode: 2, item: ""},
		{name: "the first episode of a long range", season: 4, episode: 1, item: "item-long-range"},
		{name: "the second episode of a long range", season: 4, episode: 2, item: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			index, _, _ := standJellyfinIndex(t, jellyfinDoubleFixture())
			index.prime(t.Context())

			item, found := index.itemFor(t.Context(), map[string]string{"tmdb": "2101"}, test.season, test.episode)

			if item != test.item || found != (test.item != "") {
				t.Errorf("item = %q, %v, want %q", item, found, test.item)
			}
		})
	}
}

// The index holds the span and the run time of a double, which the marks
// read to place a mark on one episode at its seconds in the file. An item
// of one episode holds no span, and the marks write it as one work.
func TestTheIndexHoldsTheSpanOfADouble(t *testing.T) {
	index, _, _ := standJellyfinIndex(t, jellyfinDoubleFixture())
	index.prime(t.Context())

	for _, test := range []struct {
		item string
		want jellyfinSpan
	}{
		{item: "item-double", want: jellyfinSpan{first: 1, last: 2, runTime: 2640}},
		{item: "item-unmarked-double", want: jellyfinSpan{}},
		{item: "item-long-range", want: jellyfinSpan{}},
		{item: "item-series-3-5", want: jellyfinSpan{}},
	} {
		t.Run(test.item, func(t *testing.T) {
			if got := index.spanOf(test.item); got != test.want {
				t.Errorf("span = %+v, want %+v", got, test.want)
			}
		})
	}
}

// A Play of the second episode plays the file from the second episode's
// start, and its status position is a position in the file. The role
// writes that position to the double, and sets played only when the whole
// file passes the watched rule, because Jellyfin's played flag covers the
// whole item.
func TestAPlayOfTheSecondEpisodeWritesItsFilePositionToTheDouble(t *testing.T) {
	fake := jellyfinDoubleFixture()
	out, _ := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "2101"}, 1, 2))

	out.status("play-1", []byte(`{"item":0,"position":"0:30:00","duration":"0:44:00"}`))
	out.tick(t.Context())
	out.status("play-1", []byte(`{"item":0,"position":"0:44:00","duration":"0:44:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 2 || fake.writes[0].item != "item-double" || fake.writes[1].item != "item-double" {
		t.Fatalf("writes = %+v, want two writes of the double", fake.writes)
	}
	if fake.writes[0].data.PlaybackPositionTicks != 18_000_000_000 || fake.writes[0].stated {
		t.Errorf("write = %+v, want the file position with no played state", fake.writes[0])
	}
	if !fake.writes[1].data.Played {
		t.Errorf("write = %+v, want played at the end of the file", fake.writes[1])
	}
}
