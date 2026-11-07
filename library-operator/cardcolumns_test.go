package main

import (
	"context"
	"testing"
)

// The card's two columns of one item, as the agent holds them.
func cardColumns(t *testing.T, agent *sqliteAgent, table, id string) []any {
	t.Helper()
	_, rows, err := agent.readAll(
		`SELECT content_rating, tagline FROM `+table+` WHERE library = ? AND id = ?`,
		"house/movies", id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("read %s %s: %v %v", table, id, rows, err)
	}
	return rows[0]
}

// A list on a screen draws the rating and the tagline of every item, so the
// scan writes both out of the body into columns of their own, and a read of
// the list needs no parse of each body.
func TestAScanWritesTheCardFieldsIntoTheirColumns(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := context.Background()
	movie := movieRow{
		Id: "movie:tmdb:1", Library: "house/movies", Kind: "movies", Title: "A Film",
		Body: movieBody{Tagline: "One line.", ContentRating: "PG"},
	}
	show := seriesRow{
		Id: "series:tvdb:2", Library: "house/movies", Kind: "series", Title: "A Show",
		Body: seriesBody{ContentRating: "TV-14"},
	}
	if _, err := catalog.UpsertMovies(ctx, []movieRow{movie}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertSeries(ctx, []seriesRow{show}); err != nil {
		t.Fatal(err)
	}

	if got := cardColumns(t, agent, "movies", movie.Id); got[0] != "PG" || got[1] != "One line." {
		t.Errorf("movie card columns = %v", got)
	}
	if got := cardColumns(t, agent, "series", show.Id); got[0] != "TV-14" || got[1] != "" {
		t.Errorf("series card columns = %v", got)
	}
}

// The nfo phase rewrites a body, and the columns follow it in the same
// statement, so the two never disagree.
func TestABodyRewriteMovesTheCardColumnsWithIt(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := context.Background()
	movie := movieRow{
		Id: "movie:tmdb:1", Library: "house/movies", Kind: "movies", Title: "A Film",
		Body: movieBody{Tagline: "Old line."},
	}
	if _, err := catalog.UpsertMovies(ctx, []movieRow{movie}); err != nil {
		t.Fatal(err)
	}

	update := bodyUpdate(movie.Library, movie.Id, movieBody{Tagline: "New line.", ContentRating: "R"}, "")
	if _, err := catalog.UpdateItemBodies(ctx, "movies", []itemUpdate{update}); err != nil {
		t.Fatal(err)
	}

	if got := cardColumns(t, agent, "movies", movie.Id); got[0] != "R" || got[1] != "New line." {
		t.Errorf("movie card columns = %v", got)
	}
}
