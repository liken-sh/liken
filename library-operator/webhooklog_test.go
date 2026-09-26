package main

// These tests read the line a webhook leaves: the Library it named and
// whether it named a folder, which the line gives as a count alone.

import (
	"testing"
)

func TestAWebhookLeavesOneLine(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "a folder",
			payload: `{"movie":{"folderPath":"/media/movies/Some Film (1999)"}}`,
			want:    "library house/movies: a webhook named 1 folder, which the next pass walks",
		},
		{
			name:    "no folder",
			payload: `{}`,
			want:    "library house/movies: a webhook named no folder, so the next pass walks the whole library",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			operator, logged := loggingOperator(t, newFakeCluster())

			postWebhook(t, operator, webhookPathPrefix+"house/movies", c.payload)

			wantOneLine(t, logged, c.want)
			if found := linesWith(logged, "Some Film"); found != nil {
				t.Errorf("a line names the folder: %q", found)
			}
		})
	}
}
