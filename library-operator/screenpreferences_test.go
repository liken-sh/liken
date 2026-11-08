package main

import "testing"

// The browser marks the audio and subtitle tracks a play of a film will
// take, so it reads the Player's preferences as media-operator resolves
// them: each field from the Player where the Player states it, and from
// the household's MediaPreferences where it does not. An empty list on
// the Player states no language and overrides the household's list.
func TestScreenPreferencesResolveThePlayerOverTheHousehold(t *testing.T) {
	household := &MediaPreferencesSpec{
		TimeZone:          "America/New_York",
		AudioLanguages:    []string{"en"},
		SubtitleLanguages: []string{"en", "es"},
		Subtitles:         "auto",
		SDH:               "prefer",
	}
	cases := []struct {
		name      string
		player    PlayerSpec
		household *MediaPreferencesSpec
		want      string
	}{
		{"no tier states a preference", PlayerSpec{}, nil, ""},
		{"a household with only a zone states no preference", PlayerSpec{}, &MediaPreferencesSpec{TimeZone: "UTC"}, ""},
		{
			"the household alone", PlayerSpec{}, household,
			`{"audioLanguages":["en"],"subtitleLanguages":["en","es"],"subtitles":"auto","sdh":"prefer"}`,
		},
		{
			"the Player's fields over the household's, field by field",
			PlayerSpec{AudioLanguages: []string{"ja"}, Subtitles: "on"}, household,
			`{"audioLanguages":["ja"],"subtitleLanguages":["en","es"],"subtitles":"on","sdh":"prefer"}`,
		},
		{
			"an empty list on the Player overrides the household's",
			PlayerSpec{SubtitleLanguages: []string{}}, household,
			`{"audioLanguages":["en"],"subtitles":"auto","sdh":"prefer"}`,
		},
		{
			"the Player alone", PlayerSpec{Subtitles: "off"}, nil,
			`{"subtitles":"off"}`,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			player := denScreen()
			player.Spec = one.player
			if got := screenPreferences(player, one.household); got != one.want {
				t.Errorf("got %s, want %s", got, one.want)
			}
		})
	}
}

// The resolved preferences reach the browser as one variable, and a
// Player with none carries no variable.
func TestScreenPodCarriesThePlayersPreferences(t *testing.T) {
	player := denScreen()
	player.Spec = PlayerSpec{Subtitles: "on", SubtitleLanguages: []string{"en"}}
	pod := buildScreenPod(player, houseLibraries(), testNamespaceCatalog(),
		testBrowserImage, testCorrosionImage, defaultTopicBase, nil)
	environment := map[string]string{}
	for _, variable := range pod.Spec.Containers[0].Env {
		environment[variable.Name] = variable.Value
	}
	if got := environment[mediaPreferencesVariable]; got != `{"subtitleLanguages":["en"],"subtitles":"on"}` {
		t.Errorf("%s = %q", mediaPreferencesVariable, got)
	}
	if _, set := browserEnvironment(denScreen())[mediaPreferencesVariable]; set {
		t.Errorf("a Player with no preferences set %s", mediaPreferencesVariable)
	}
}
