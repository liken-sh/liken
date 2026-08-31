package main

// aliasRowsForItem extends the canonical-order aliases with
// every other provider id, so a title's less-common ids still resolve
// it. These tests fix that extension and its deterministic order.

import (
	"reflect"
	"testing"
)

func TestAliasRowsForItemAddsEveryProvider(t *testing.T) {
	providers := map[string]string{"tmdb": "603", "imdb": "tt0133093", "tvdb": "12345"}
	got := aliasRowsForItem("movies", scopeMovie, providers, "the-matrix", "movie:tmdb:603")
	want := []aliasRow{
		{Library: "movies", Alias: "movie:tmdb:603", Item: "movie:tmdb:603", Source: aliasSourceProvider},
		{Library: "movies", Alias: "movie:imdb:tt0133093", Item: "movie:tmdb:603", Source: aliasSourceProvider},
		{Library: "movies", Alias: "movie:path:the-matrix", Item: "movie:tmdb:603", Source: aliasSourceFolder},
		{Library: "movies", Alias: "movie:tvdb:12345", Item: "movie:tmdb:603", Source: aliasSourceProvider},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("aliasRowsForItem = %+v, want %+v", got, want)
	}
}

// Two extra providers are added in sorted order, so a re-walk of the
// same sidecar writes the same rows.
func TestAliasRowsForItemIsDeterministic(t *testing.T) {
	providers := map[string]string{"tmdb": "1", "zdb": "z", "adb": "a"}
	first := aliasRowsForItem("movies", scopeMovie, providers, "", "movie:tmdb:1")
	second := aliasRowsForItem("movies", scopeMovie, providers, "", "movie:tmdb:1")
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two passes differ:\n%+v\n%+v", first, second)
	}
	if first[len(first)-2].Alias != "movie:adb:a" || first[len(first)-1].Alias != "movie:zdb:z" {
		t.Errorf("extra providers are not sorted: %+v", first)
	}
}

// A provider with an empty value is not made into an alias.
func TestAliasRowsForItemDropsEmptyProviderValues(t *testing.T) {
	providers := map[string]string{"tmdb": "1", "blank": ""}
	got := aliasRowsForItem("movies", scopeMovie, providers, "", "movie:tmdb:1")
	for _, row := range got {
		if row.Alias == "movie:blank:" {
			t.Errorf("an empty provider value became an alias: %+v", got)
		}
	}
}
