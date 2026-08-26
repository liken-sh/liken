package main

// These tests cover the two schemes the resolver handles and what each
// one costs the playback pod: an https URI costs an argument, and an
// nfs URI costs a share of a mount, a rewritten argument, and a
// rewritten logo when the presentation carries one.

import (
	"reflect"
	"testing"
)

// mediaItems builds a PlayItem list from bare media URIs, for the cases
// that carry no presentation.
func mediaItems(uris ...string) []PlayItem {
	items := make([]PlayItem, len(uris))
	for i, uri := range uris {
		items[i] = PlayItem{URI: uri}
	}
	return items
}

func TestResolvePassesAnHTTPSURIThrough(t *testing.T) {
	resolved, err := resolvePlay(mediaItems("https://films.example/movies/film.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://films.example/movies/film.mkv"}
	if !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if len(resolved.Volumes) != 0 {
		t.Errorf("volumes = %+v, want none", resolved.Volumes)
	}
	if len(resolved.Mounts) != 0 {
		t.Errorf("mounts = %+v, want none", resolved.Mounts)
	}
}

// An nfs URI names a file, and the pod mounts the directory that
// holds it, read-only in both places.
func TestResolveMountsTheDirectoryThatHoldsTheFile(t *testing.T) {
	resolved, err := resolvePlay(mediaItems("nfs://nas.example/export/dir/film.mkv"))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/media/1/film.mkv"}
	if !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	volumes := []Volume{{
		Name: "media-1",
		NFS:  &NFSVolumeSource{Server: "nas.example", Path: "/export/dir", ReadOnly: true},
	}}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
	mounts := []VolumeMount{{Name: "media-1", MountPath: "/media/1", ReadOnly: true}}
	if !reflect.DeepEqual(resolved.Mounts, mounts) {
		t.Errorf("mounts = %+v, want %+v", resolved.Mounts, mounts)
	}
}

// Two files in one directory share one mount of that directory, and
// each keeps its own name under it.
func TestResolveCommonAncestorOfTwoFilesInOneDirectory(t *testing.T) {
	resolved, err := resolvePlay(mediaItems(
		"nfs://nas.example/export/dir/first.mkv",
		"nfs://nas.example/export/dir/second.mkv",
	))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/media/1/first.mkv", "/media/1/second.mkv"}
	if !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	volumes := []Volume{{
		Name: "media-1",
		NFS:  &NFSVolumeSource{Server: "nas.example", Path: "/export/dir", ReadOnly: true},
	}}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
}

// Files in sibling directories share one mount of the parent, and each
// carries its own subdirectory under it.
func TestResolveMountsTheParentOfSiblingDirectories(t *testing.T) {
	resolved, err := resolvePlay(mediaItems(
		"nfs://nas.example/export/films/film.mkv",
		"nfs://nas.example/export/shows/episode.mkv",
	))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/media/1/films/film.mkv", "/media/1/shows/episode.mkv"}
	if !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	volumes := []Volume{{
		Name: "media-1",
		NFS:  &NFSVolumeSource{Server: "nas.example", Path: "/export", ReadOnly: true},
	}}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
	if len(resolved.Mounts) != 1 {
		t.Fatalf("mounts = %+v, want one", resolved.Mounts)
	}
}

// Two servers cost two mounts, one common ancestor each, numbered by
// first appearance.
func TestResolveMountsOneCommonAncestorPerServer(t *testing.T) {
	resolved, err := resolvePlay(mediaItems(
		"nfs://films.example/export/films/film.mkv",
		"nfs://shows.example/export/shows/episode.mkv",
		"nfs://films.example/export/films/other.mkv",
	))
	if err != nil {
		t.Fatal(err)
	}

	items := []string{"/media/1/film.mkv", "/media/2/episode.mkv", "/media/1/other.mkv"}
	if !reflect.DeepEqual(resolved.Items, items) {
		t.Errorf("items = %v, want %v", resolved.Items, items)
	}
	volumes := []Volume{
		{Name: "media-1", NFS: &NFSVolumeSource{Server: "films.example", Path: "/export/films", ReadOnly: true}},
		{Name: "media-2", NFS: &NFSVolumeSource{Server: "shows.example", Path: "/export/shows", ReadOnly: true}},
	}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
	mounts := []VolumeMount{
		{Name: "media-1", MountPath: "/media/1", ReadOnly: true},
		{Name: "media-2", MountPath: "/media/2", ReadOnly: true},
	}
	if !reflect.DeepEqual(resolved.Mounts, mounts) {
		t.Errorf("mounts = %+v, want %+v", resolved.Mounts, mounts)
	}
}

// The playlist plays in spec order, so a mixed list keeps its order
// however each item resolves.
func TestResolveKeepsAMixedListInSpecOrder(t *testing.T) {
	resolved, err := resolvePlay(mediaItems(
		"https://films.example/trailer.mkv",
		"nfs://nas.example/export/films/film.mkv",
		"https://films.example/credits.mkv",
	))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"https://films.example/trailer.mkv",
		"/media/1/film.mkv",
		"https://films.example/credits.mkv",
	}
	if !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if len(resolved.Volumes) != 1 || len(resolved.Mounts) != 1 {
		t.Errorf("volumes = %+v, mounts = %+v, want one of each", resolved.Volumes, resolved.Mounts)
	}
}

// A lone https media item mounts nothing and rewrites nothing.
func TestResolveALoneHTTPSItemMountsNothing(t *testing.T) {
	resolved, err := resolvePlay(mediaItems("https://films.example/film.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Volumes) != 0 || len(resolved.Mounts) != 0 {
		t.Errorf("volumes = %+v, mounts = %+v, want none", resolved.Volumes, resolved.Mounts)
	}
	if len(resolved.Logos) != 1 || resolved.Logos[0] != "" {
		t.Errorf("logos = %v, want one empty", resolved.Logos)
	}
}

// A logo beside the media shares the media's mount, because the common
// ancestor covers them both, and its path rewrites under that mount.
func TestResolveALogoBesideTheMediaSharesTheMount(t *testing.T) {
	resolved, err := resolvePlay([]PlayItem{{
		URI: "nfs://nas.example/export/film/film.mkv",
		Presentation: &Presentation{
			Logo: "nfs://nas.example/export/film/logo.png",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"/media/1/film.mkv"}; !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if want := []string{"/media/1/logo.png"}; !reflect.DeepEqual(resolved.Logos, want) {
		t.Errorf("logos = %v, want %v", resolved.Logos, want)
	}
	if len(resolved.Volumes) != 1 || len(resolved.Mounts) != 1 {
		t.Fatalf("volumes = %+v, mounts = %+v, want one of each", resolved.Volumes, resolved.Mounts)
	}
	volume := Volume{Name: "media-1", NFS: &NFSVolumeSource{Server: "nas.example", Path: "/export/film", ReadOnly: true}}
	if !reflect.DeepEqual(resolved.Volumes[0], volume) {
		t.Errorf("volume = %+v, want %+v", resolved.Volumes[0], volume)
	}
}

// The cover art resolves the way the logo does: an nfs reference rewrites
// under the media's own mount, and an https reference stays a URL.
func TestResolveTheCoverArtTheWayTheLogoResolves(t *testing.T) {
	cases := []struct {
		name string
		art  string
		want string
	}{
		{name: "a cover beside the album", art: "nfs://nas.example/music/album/cover.jpg", want: "/media/1/cover.jpg"},
		{name: "a cover on the web", art: "https://art.example/cover.jpg", want: "https://art.example/cover.jpg"},
		{name: "no cover at all", art: "", want: ""},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			resolved, err := resolvePlay([]PlayItem{{
				URI:          "nfs://nas.example/music/album/track.flac",
				Presentation: &Presentation{Type: "music", Art: each.art},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{each.want}; !reflect.DeepEqual(resolved.Arts, want) {
				t.Errorf("arts = %v, want %v", resolved.Arts, want)
			}
			if want := []string{"/media/1/track.flac"}; !reflect.DeepEqual(resolved.Items, want) {
				t.Errorf("items = %v, want %v", resolved.Items, want)
			}
		})
	}
}

// A directory the pod does not expand is refused, because mpv would expand it
// itself and add playlist entries the operator never counted, which would land
// every later item's block on the wrong track. The error names both ways out.
func TestResolveRefusesADirectoryThatIsNoAlbum(t *testing.T) {
	cases := []struct {
		name         string
		presentation *Presentation
	}{
		{name: "no presentation at all"},
		{name: "music with no hint", presentation: &Presentation{Type: "music"}},
		{name: "the album hint on a film", presentation: &Presentation{Type: "video", Hint: "album"}},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			_, err := resolvePlay([]PlayItem{{
				URI:          "nfs://nas.example/music/None Shall Pass/",
				Presentation: each.presentation,
			}})
			if err == nil {
				t.Fatal("a directory that is no album resolved")
			}
			want := `the URI "nfs://nas.example/music/None Shall Pass/" names a directory; ` +
				"mark the item as an album with type music and hint album, or name a file"
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

// A music album is one item and one directory, and it mounts the way a file
// item mounts: the parent is the volume, and the item is the directory under
// it. A trailing slash changes nothing.
func TestResolveAnAlbumDirectory(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{name: "a directory", uri: "nfs://nas.example/music/None Shall Pass"},
		{name: "a directory with a trailing slash", uri: "nfs://nas.example/music/None Shall Pass/"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			resolved, err := resolvePlay([]PlayItem{{
				URI:          each.uri,
				Presentation: &Presentation{Type: "music", Hint: "album"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"/media/1/None Shall Pass"}; !reflect.DeepEqual(resolved.Items, want) {
				t.Errorf("items = %v, want %v", resolved.Items, want)
			}
			volume := Volume{Name: "media-1", NFS: &NFSVolumeSource{Server: "nas.example", Path: "/music", ReadOnly: true}}
			if len(resolved.Volumes) != 1 || !reflect.DeepEqual(resolved.Volumes[0], volume) {
				t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volume)
			}
		})
	}
}

// An https logo stays a URL for the bridge to fetch, and the media
// beside it still mounts.
func TestResolveAnHTTPSLogoStaysAURL(t *testing.T) {
	resolved, err := resolvePlay([]PlayItem{{
		URI: "nfs://nas.example/export/film/film.mkv",
		Presentation: &Presentation{
			Logo: "https://art.example/logo.png",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"/media/1/film.mkv"}; !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if want := []string{"https://art.example/logo.png"}; !reflect.DeepEqual(resolved.Logos, want) {
		t.Errorf("logos = %v, want %v", resolved.Logos, want)
	}
}

// An item with no presentation, and an item whose presentation carries
// no logo, both resolve to an empty logo.
func TestResolveLeavesAnAbsentLogoEmpty(t *testing.T) {
	resolved, err := resolvePlay([]PlayItem{
		{URI: "nfs://nas.example/export/film/film.mkv"},
		{URI: "nfs://nas.example/export/film/other.mkv", Presentation: &Presentation{Title: "Other"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", ""}
	if !reflect.DeepEqual(resolved.Logos, want) {
		t.Errorf("logos = %v, want %v", resolved.Logos, want)
	}
}

// An unresolvable scheme's error names the two the operator does
// resolve, because the reader is writing a Play and needs the
// vocabulary.
func TestResolveNamesTheSchemesItResolves(t *testing.T) {
	_, err := resolvePlay(mediaItems("rtsp://camera.example/stream"))
	if err == nil {
		t.Fatal("an unknown scheme resolved")
	}
	want := "the scheme rtsp:// is not one the operator resolves; it resolves https:// and nfs://"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestResolveRefusesAURIItCannotMount(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{name: "no scheme at all", uri: "/export/films/film.mkv"},
		{name: "an nfs URI with no server", uri: "nfs:///export/films/film.mkv"},
		{name: "an nfs URI with no directory to mount", uri: "nfs://nas.example/film.mkv"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resolved, err := resolvePlay(mediaItems(c.uri))
			if err == nil {
				t.Fatalf("%q resolved to %+v", c.uri, resolved)
			}
			// A refusal resolves nothing at all, so a Play never
			// runs on half a playlist.
			if len(resolved.Items) != 0 || len(resolved.Volumes) != 0 || len(resolved.Mounts) != 0 {
				t.Errorf("a refused list resolved %+v", resolved)
			}
		})
	}
}

// A logo URI the resolver cannot mount fails the Play the same way a
// media URI does, because both share the mount.
func TestResolveRefusesALogoItCannotMount(t *testing.T) {
	_, err := resolvePlay([]PlayItem{{
		URI:          "nfs://nas.example/export/film/film.mkv",
		Presentation: &Presentation{Logo: "rtsp://camera.example/logo"},
	}})
	if err == nil {
		t.Fatal("a logo with an unresolvable scheme resolved")
	}
}

// Three tiers resolve each field on its own. A more specific tier wins, and a
// field no tier states resolves to nothing.
func TestResolvePreferencesTierWins(t *testing.T) {
	play := &PlaySpec{
		AudioLanguages:    []string{"play-a"},
		SubtitleLanguages: []string{"play-s"},
		Subtitles:         subtitlesOn,
	}
	player := &PlayerSpec{
		AudioLanguages:    []string{"player-a"},
		SubtitleLanguages: []string{"player-s"},
		Subtitles:         subtitlesOff,
	}
	defaults := &MediaPreferencesSpec{
		AudioLanguages:    []string{"default-a"},
		SubtitleLanguages: []string{"default-s"},
		Subtitles:         subtitlesAuto,
	}

	cases := []struct {
		name     string
		play     *PlaySpec
		player   *PlayerSpec
		defaults *MediaPreferencesSpec
		want     resolvedPreferences
	}{
		{
			name: "the Play wins over the Player and the default",
			play: play, player: player, defaults: defaults,
			want: resolvedPreferences{
				AudioLanguages:    []string{"play-a"},
				SubtitleLanguages: []string{"play-s"},
				Subtitles:         subtitlesOn,
			},
		},
		{
			name:   "the Player wins when the Play states nothing",
			player: player, defaults: defaults,
			want: resolvedPreferences{
				AudioLanguages:    []string{"player-a"},
				SubtitleLanguages: []string{"player-s"},
				Subtitles:         subtitlesOff,
			},
		},
		{
			name:     "the default wins when neither the Play nor the Player states anything",
			defaults: defaults,
			want: resolvedPreferences{
				AudioLanguages:    []string{"default-a"},
				SubtitleLanguages: []string{"default-s"},
				Subtitles:         subtitlesAuto,
			},
		},
		{
			name: "a field no tier states resolves to nothing",
			want: resolvedPreferences{},
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := resolvePreferences(one.play, one.player, one.defaults)
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("preferences = %+v, want %+v", got, one.want)
			}
		})
	}
}

// Each field resolves on its own. The Play sets subtitles while the default
// still supplies the languages.
func TestResolvePreferencesResolvesEachFieldOnItsOwn(t *testing.T) {
	play := &PlaySpec{Subtitles: subtitlesOn}
	defaults := &MediaPreferencesSpec{
		AudioLanguages:    []string{"en", "ja"},
		SubtitleLanguages: []string{"en"},
		Subtitles:         subtitlesAuto,
	}

	got := resolvePreferences(play, nil, defaults)
	want := resolvedPreferences{
		AudioLanguages:    []string{"en", "ja"},
		SubtitleLanguages: []string{"en"},
		Subtitles:         subtitlesOn,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preferences = %+v, want %+v", got, want)
	}
}

// An empty list at a tier is a stated no preference, and it overrides a lower
// tier's list back to nothing.
func TestResolvePreferencesEmptyListOverridesALowerTier(t *testing.T) {
	play := &PlaySpec{AudioLanguages: []string{}}
	defaults := &MediaPreferencesSpec{AudioLanguages: []string{"en"}}

	got := resolvePreferences(play, nil, defaults)
	if got.AudioLanguages == nil || len(got.AudioLanguages) != 0 {
		t.Errorf("audioLanguages = %#v, want a stated empty list", got.AudioLanguages)
	}
}

// A nil source is a tier that does not exist. Resolution reads the tiers that do.
func TestResolvePreferencesSkipsNilTiers(t *testing.T) {
	defaults := &MediaPreferencesSpec{AudioLanguages: []string{"en"}}

	got := resolvePreferences(nil, nil, defaults)
	want := resolvedPreferences{AudioLanguages: []string{"en"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preferences = %+v, want %+v", got, want)
	}
}

// The timezone is a household setting, so it resolves from the default tier
// alone and reads nothing off the Play or the Player.
func TestResolvePreferencesTimeZoneReadsTheDefaultTierOnly(t *testing.T) {
	defaults := &MediaPreferencesSpec{TimeZone: "America/New_York"}

	got := resolvePreferences(&PlaySpec{}, &PlayerSpec{}, defaults)
	if got.TimeZone != "America/New_York" {
		t.Errorf("timeZone = %q, want %q", got.TimeZone, "America/New_York")
	}
}

// With no default MediaPreferences the timezone resolves to nothing, so the pod
// gets no TZ.
func TestResolvePreferencesTimeZoneUnsetWithoutDefaults(t *testing.T) {
	got := resolvePreferences(&PlaySpec{}, &PlayerSpec{}, nil)
	if got.TimeZone != "" {
		t.Errorf("timeZone = %q, want empty", got.TimeZone)
	}
}

// fadeAfter builds an IdlePolicy that states one fade window.
func fadeAfter(seconds int64) *IdlePolicy {
	return &IdlePolicy{FadeAfterSeconds: &seconds}
}

// The fade window resolves the Player first and the household default
// second, and a cluster that states neither takes the built-in ten minutes.
// Zero is a stated value and not an absent one, so a kiosk that must never
// dim states it and keeps its screen.
func TestResolveIdleFadeAfterTierWins(t *testing.T) {
	cases := []struct {
		name     string
		player   *IdlePolicy
		defaults *IdlePolicy
		want     int64
	}{
		{name: "the Player over the default", player: fadeAfter(60), defaults: fadeAfter(900), want: 60},
		{name: "the default where the Player states none", defaults: fadeAfter(900), want: 900},
		{name: "the built-in where neither states one", want: defaultFadeAfterSeconds},
		{name: "an empty block on the Player", player: &IdlePolicy{}, defaults: fadeAfter(900), want: 900},
		{name: "zero on the Player", player: fadeAfter(0), defaults: fadeAfter(900), want: 0},
		{name: "zero on the default", defaults: fadeAfter(0), want: 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults).FadeAfterSeconds, one.want)
		})
	}
}

// offAfter builds an IdlePolicy that states one hardware window.
func offAfter(seconds int64) *IdlePolicy {
	return &IdlePolicy{OffAfterSeconds: &seconds}
}

// The hardware window resolves the Player first and the household
// default second, and a cluster that states neither keeps the panel
// lit, because darkening hardware is opt-in twice. Zero is a stated
// value and not an absent one.
func TestResolveIdleOffAfterTierWins(t *testing.T) {
	cases := []struct {
		name     string
		player   *IdlePolicy
		defaults *IdlePolicy
		want     int64
	}{
		{name: "the Player over the default", player: offAfter(120), defaults: offAfter(1800), want: 120},
		{name: "the default where the Player states none", defaults: offAfter(1800), want: 1800},
		{name: "the built-in where neither states one", want: defaultOffAfterSeconds},
		{name: "an empty block on the Player", player: &IdlePolicy{}, defaults: offAfter(1800), want: 1800},
		{name: "zero on the Player", player: offAfter(0), defaults: offAfter(1800), want: 0},
		{name: "zero on the default", defaults: offAfter(0), want: 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults).OffAfterSeconds, one.want)
		})
	}
}

// The mode resolves the same way, field by field, and an unstated
// mode takes the backlight, the state that always answers DDC.
func TestResolveIdleOffModeTierWins(t *testing.T) {
	cases := []struct {
		name     string
		player   *IdlePolicy
		defaults *IdlePolicy
		want     string
	}{
		{
			name:     "the Player over the default",
			player:   &IdlePolicy{OffMode: offModePower},
			defaults: &IdlePolicy{OffMode: offModeBacklight},
			want:     offModePower,
		},
		{
			name:     "the default where the Player states none",
			defaults: &IdlePolicy{OffMode: offModePower},
			want:     offModePower,
		},
		{name: "the built-in where neither states one", want: defaultOffMode},
		{
			name:     "an empty mode on the Player",
			player:   &IdlePolicy{OffMode: ""},
			defaults: &IdlePolicy{OffMode: offModePower},
			want:     offModePower,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults).OffMode, one.want)
		})
	}
}

// Each field resolves on its own, so a Player that states one still
// inherits the other two from the household.
func TestResolveIdleSettlesEachFieldOnItsOwn(t *testing.T) {
	player := &IdlePolicy{OffMode: offModePower}
	defaults := &IdlePolicy{FadeAfterSeconds: ptr(int64(900)), OffAfterSeconds: ptr(int64(1800))}

	got := resolveIdle(player, defaults)

	mustMatch(t, got.FadeAfterSeconds, int64(900))
	mustMatch(t, got.OffAfterSeconds, int64(1800))
	mustMatch(t, got.OffMode, offModePower)
}

// ptr is one value's address, for a tier that states a number.
func ptr[T any](value T) *T {
	return &value
}
