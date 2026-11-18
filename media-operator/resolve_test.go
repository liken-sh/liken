package main

import (
	"reflect"
	"strings"
	"testing"
)

// mediaItems builds a PlayItem list from media URIs, with no
// presentation.
func mediaItems(uris ...string) []PlayItem {
	items := make([]PlayItem, len(uris))
	for i, uri := range uris {
		items[i] = PlayItem{URI: uri}
	}
	return items
}

func TestResolvePassesAnHTTPSURIThrough(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems("https://films.example/movies/film.mkv"), nil, frame{})
	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
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

// For one nfs:// file, the pod mounts the file's directory read-only.
func TestResolveMountsTheDirectoryThatHoldsTheFile(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems("nfs://nas.example/export/dir/film.mkv"), nil, frame{})
	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
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

// Two files in one directory share one mount of that directory.
func TestResolveCommonAncestorOfTwoFilesInOneDirectory(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems(
		"nfs://nas.example/export/dir/first.mkv",
		"nfs://nas.example/export/dir/second.mkv",
	), nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
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

// Files in sibling directories share one mount of the parent directory,
// and each path includes its own subdirectory.
func TestResolveMountsTheParentOfSiblingDirectories(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems(
		"nfs://nas.example/export/films/film.mkv",
		"nfs://nas.example/export/shows/episode.mkv",
	), nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
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

// Two servers get two mounts, each at its own common ancestor, numbered
// in the order the playlist first uses them.
func TestResolveMountsOneCommonAncestorPerServer(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems(
		"nfs://films.example/export/films/film.mkv",
		"nfs://shows.example/export/shows/episode.mkv",
		"nfs://films.example/export/films/other.mkv",
	), nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
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

func TestResolveKeepsAMixedListInSpecOrder(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems(
		"https://films.example/trailer.mkv",
		"nfs://nas.example/export/films/film.mkv",
		"https://films.example/credits.mkv",
	), nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
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

func TestResolveALoneHTTPSItemMountsNothing(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems("https://films.example/film.mkv"), nil, frame{})
	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}
	if len(resolved.Volumes) != 0 || len(resolved.Mounts) != 0 {
		t.Errorf("volumes = %+v, mounts = %+v, want none", resolved.Volumes, resolved.Mounts)
	}
	if len(resolved.Logos) != 1 || resolved.Logos[0] != "" {
		t.Errorf("logos = %v, want one empty", resolved.Logos)
	}
}

// A logo in the media's directory is rewritten under the media's mount.
func TestResolveALogoBesideTheMediaSharesTheMount(t *testing.T) {
	resolved, err := resolvePlay("house", []PlayItem{{
		URI: "nfs://nas.example/export/film/film.mkv",
		Presentation: &Presentation{
			Logo: "nfs://nas.example/export/film/logo.png",
		},
	}}, nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
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

// The cover resolves like the logo. An nfs:// cover is rewritten under
// the media's mount, and an https:// cover stays a URL.
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
			resolved, err := resolvePlay("house", []PlayItem{{
				URI:          "nfs://nas.example/music/album/track.flac",
				Presentation: &Presentation{Type: "music", Art: each.art},
			}}, nil, frame{})

			if err != nil {
				t.Fatalf("resolving the Play: %v", err)
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

// A directory item that is not an album is an error, and the error
// gives both ways to fix the Play.
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
			_, err := resolvePlay("house", []PlayItem{{
				URI:          "nfs://nas.example/music/None Shall Pass/",
				Presentation: each.presentation,
			}}, nil, frame{})

			if err == nil {
				t.Fatal("a directory that is not an album resolved, want an error")
			}
			if want := "is a directory and the item is not an album"; !strings.Contains(err.Error(), want) {
				t.Errorf("error is %q, want it to contain %q", err.Error(), want)
			}
		})
	}
}

// An album item is a directory. Its parent directory is the volume, as
// for a file, and a trailing slash makes no difference.
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
			resolved, err := resolvePlay("house", []PlayItem{{
				URI:          each.uri,
				Presentation: &Presentation{Type: "music", Hint: "album"},
			}}, nil, frame{})

			if err != nil {
				t.Fatalf("resolving the Play: %v", err)
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

// A claim:// item mounts the whole claim read-only, and the path of the
// URI is the path under that mount.
func TestResolveMountsAClaimAtItsRoot(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems("claim://house/films/movies/film.mkv"), nil, frame{})
	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}

	if want := []string{"/media/1/movies/film.mkv"}; !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	volumes := []Volume{{
		Name:                  "media-1",
		PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: "films", ReadOnly: true},
	}}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
	mounts := []VolumeMount{{Name: "media-1", MountPath: "/media/1", ReadOnly: true}}
	if !reflect.DeepEqual(resolved.Mounts, mounts) {
		t.Errorf("mounts = %+v, want %+v", resolved.Mounts, mounts)
	}
}

// A film, its logo, and its trickplay sheet on one claim share one
// mount, wherever each is under the claim.
func TestResolveOneClaimCostsOneMount(t *testing.T) {
	resolved, err := resolvePlay("house", []PlayItem{{
		URI: "claim://house/library/movies/film/film.mkv",
		Presentation: &Presentation{
			Logo:      "claim://house/library/art/film/logo.png",
			Trickplay: "claim://house/library/movies/film/film.trickplay",
		},
	}}, nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}

	if want := []string{"/media/1/movies/film/film.mkv"}; !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if want := []string{"/media/1/art/film/logo.png"}; !reflect.DeepEqual(resolved.Logos, want) {
		t.Errorf("logos = %v, want %v", resolved.Logos, want)
	}
	if want := []string{"/media/1/movies/film/film.trickplay"}; !reflect.DeepEqual(resolved.Trickplays, want) {
		t.Errorf("trickplays = %v, want %v", resolved.Trickplays, want)
	}
	volumes := []Volume{{
		Name:                  "media-1",
		PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: "library", ReadOnly: true},
	}}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
	if len(resolved.Mounts) != 1 {
		t.Errorf("mounts = %+v, want one", resolved.Mounts)
	}
}

// The spans file is in the film's folder and the .contributors
// directory is at the library's root, so the nfs mount is the library's
// root.
func TestResolveTheAppearancesAndTheirPortraitsShareTheLibraryMount(t *testing.T) {
	resolved, err := resolvePlay("house", []PlayItem{{
		URI: "nfs://nas/movies/Action/Film/Film.mkv",
		Presentation: &Presentation{
			Appearances:  "nfs://nas/movies/Action/Film/.liken/appearances/Film.mkv.spans.json",
			Contributors: "nfs://nas/movies/.contributors",
		},
	}}, nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}
	if want := []string{"/media/1/Action/Film/Film.mkv"}; !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if want := []string{"/media/1/Action/Film/.liken/appearances/Film.mkv.spans.json"}; !reflect.DeepEqual(resolved.Appearances, want) {
		t.Errorf("appearances = %v, want %v", resolved.Appearances, want)
	}
	if want := []string{"/media/1/.contributors"}; !reflect.DeepEqual(resolved.Contributors, want) {
		t.Errorf("contributors = %v, want %v", resolved.Contributors, want)
	}
	volumes := []Volume{{
		Name: "media-1",
		NFS:  &NFSVolumeSource{Server: "nas", Path: "/movies", ReadOnly: true},
	}}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
}

// Two claims get two mounts, numbered in the order the playlist first
// uses them.
func TestResolveMountsOneVolumePerClaim(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems(
		"claim://house/films/movies/film.mkv",
		"claim://house/shows/episodes/episode.mkv",
		"claim://house/films/movies/other.mkv",
	), nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}

	items := []string{"/media/1/movies/film.mkv", "/media/2/episodes/episode.mkv", "/media/1/movies/other.mkv"}
	if !reflect.DeepEqual(resolved.Items, items) {
		t.Errorf("items = %v, want %v", resolved.Items, items)
	}
	volumes := []Volume{
		{Name: "media-1", PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: "films", ReadOnly: true}},
		{Name: "media-2", PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: "shows", ReadOnly: true}},
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

// Servers and claims share one numbering of mounts, in the order the
// playlist first uses each of them.
func TestResolveNumbersServersAndClaimsTogether(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems(
		"nfs://nas.example/export/films/first.mkv",
		"claim://house/library/movies/second.mkv",
		"nfs://nas.example/export/films/third.mkv",
		"claim://house/archive/clips/fourth.mkv",
	), nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}

	items := []string{
		"/media/1/first.mkv",
		"/media/2/movies/second.mkv",
		"/media/1/third.mkv",
		"/media/3/clips/fourth.mkv",
	}
	if !reflect.DeepEqual(resolved.Items, items) {
		t.Errorf("items = %v, want %v", resolved.Items, items)
	}
	volumes := []Volume{
		{Name: "media-1", NFS: &NFSVolumeSource{Server: "nas.example", Path: "/export/films", ReadOnly: true}},
		{Name: "media-2", PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: "library", ReadOnly: true}},
		{Name: "media-3", PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: "archive", ReadOnly: true}},
	}
	if !reflect.DeepEqual(resolved.Volumes, volumes) {
		t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volumes)
	}
}

func TestResolveAnAlbumDirectoryOnAClaim(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{name: "a directory", uri: "claim://house/music/albums/Third Album"},
		{name: "a directory with a trailing slash", uri: "claim://house/music/albums/Third Album/"},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			resolved, err := resolvePlay("house", []PlayItem{{
				URI:          each.uri,
				Presentation: &Presentation{Type: "music", Hint: "album"},
			}}, nil, frame{})

			if err != nil {
				t.Fatalf("resolving the Play: %v", err)
			}
			if want := []string{"/media/1/albums/Third Album"}; !reflect.DeepEqual(resolved.Items, want) {
				t.Errorf("items = %v, want %v", resolved.Items, want)
			}
			volume := Volume{
				Name:                  "media-1",
				PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{ClaimName: "music", ReadOnly: true},
			}
			if len(resolved.Volumes) != 1 || !reflect.DeepEqual(resolved.Volumes[0], volume) {
				t.Errorf("volumes = %+v, want %+v", resolved.Volumes, volume)
			}
		})
	}
}

func TestResolveRefusesAClaimDirectoryThatIsNoAlbum(t *testing.T) {
	_, err := resolvePlay("house", mediaItems("claim://house/music/albums/Third Album/"), nil, frame{})
	if err == nil {
		t.Fatal("a directory that is not an album resolved, want an error")
	}
	if want := "is a directory and the item is not an album"; !strings.Contains(err.Error(), want) {
		t.Errorf("error is %q, want it to contain %q", err.Error(), want)
	}
}

// A URI written without a namespace, as claim://<claim>/<path>, parses
// the claim as the namespace and fails one of the other checks.
func TestResolveRefusesAClaimURIItCannotMount(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "no namespace",
			uri:  "claim:///films/movies/film.mkv",
			want: "has no namespace",
		},
		{
			name: "no claim name",
			uri:  "claim://house//movies/film.mkv",
			want: "has no claim name",
		},
		{
			name: "no path in the claim",
			uri:  "claim://house/films",
			want: "has no path in the claim",
		},
		{
			name: "another namespace",
			uri:  "claim://studio/films/movies/film.mkv",
			want: "names a claim in the namespace studio",
		},
		{
			name: "no namespace segment and a path",
			uri:  "claim://films/movies/film.mkv",
			want: "names a claim in the namespace films",
		},
		{
			name: "no namespace segment and a file",
			uri:  "claim://films/film.mkv",
			want: "has no path in the claim",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			resolved, err := resolvePlay("house", mediaItems(each.uri), nil, frame{})
			if err == nil {
				t.Fatalf("%q resolved to %+v, want an error", each.uri, resolved)
			}
			if !strings.Contains(err.Error(), each.want) {
				t.Errorf("error is %q, want it to contain %q", err.Error(), each.want)
			}
			if len(resolved.Items) != 0 || len(resolved.Volumes) != 0 || len(resolved.Mounts) != 0 {
				t.Errorf("a refused list resolved to %+v, want no items, volumes, or mounts", resolved)
			}
		})
	}
}

// An https:// logo stays a URL, and the media with it still mounts.
func TestResolveAnHTTPSLogoStaysAURL(t *testing.T) {
	resolved, err := resolvePlay("house", []PlayItem{{
		URI: "nfs://nas.example/export/film/film.mkv",
		Presentation: &Presentation{
			Logo: "https://art.example/logo.png",
		},
	}}, nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}

	if want := []string{"/media/1/film.mkv"}; !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if want := []string{"https://art.example/logo.png"}; !reflect.DeepEqual(resolved.Logos, want) {
		t.Errorf("logos = %v, want %v", resolved.Logos, want)
	}
}

func TestResolveLeavesAnAbsentLogoEmpty(t *testing.T) {
	resolved, err := resolvePlay("house", []PlayItem{
		{URI: "nfs://nas.example/export/film/film.mkv"},
		{URI: "nfs://nas.example/export/film/other.mkv", Presentation: &Presentation{Title: "Other"}},
	}, nil, frame{})

	if err != nil {
		t.Fatalf("resolving the Play: %v", err)
	}
	want := []string{"", ""}
	if !reflect.DeepEqual(resolved.Logos, want) {
		t.Errorf("logos = %v, want %v", resolved.Logos, want)
	}
}

// The error for an unknown scheme lists the schemes that work, so the
// author of the Play can correct the URI.
func TestResolveNamesTheSchemesItResolves(t *testing.T) {
	_, err := resolvePlay("house", mediaItems("rtsp://camera.example/stream"), nil, frame{})
	if err == nil {
		t.Fatal("the scheme rtsp:// resolved, want an error")
	}
	if want := "the scheme rtsp:// is not one the operator resolves"; !strings.Contains(err.Error(), want) {
		t.Errorf("error is %q, want it to contain %q", err.Error(), want)
	}
	for _, scheme := range []string{"https://", "nfs://", "claim://", "pattern://"} {
		if !strings.Contains(err.Error(), scheme) {
			t.Errorf("error is %q, want it to list %s", err.Error(), scheme)
		}
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
			resolved, err := resolvePlay("house", mediaItems(c.uri), nil, frame{})
			if err == nil {
				t.Fatalf("%q resolved to %+v, want an error", c.uri, resolved)
			}
			// An error returns no items, volumes, or mounts, so a Play
			// never runs with part of its playlist.
			if len(resolved.Items) != 0 || len(resolved.Volumes) != 0 || len(resolved.Mounts) != 0 {
				t.Errorf("a refused list resolved to %+v, want no items, volumes, or mounts", resolved)
			}
		})
	}
}

func TestResolveRefusesALogoItCannotMount(t *testing.T) {
	_, err := resolvePlay("house", []PlayItem{{
		URI:          "nfs://nas.example/export/film/film.mkv",
		Presentation: &Presentation{Logo: "rtsp://camera.example/logo"},
	}}, nil, frame{})

	if err == nil {
		t.Fatal("a logo with the scheme rtsp:// resolved, want an error")
	}
}

func TestResolvePreferencesPreferPlayOverPlayerOverDefault(t *testing.T) {
	play := &PlaySpec{
		AudioLanguages:    []string{"play-a"},
		SubtitleLanguages: []string{"play-s"},
		Subtitles:         subtitlesOn,
		SDH:               sdhPrefer,
	}
	player := &PlayerSpec{
		AudioLanguages:    []string{"player-a"},
		SubtitleLanguages: []string{"player-s"},
		Subtitles:         subtitlesOff,
		SDH:               sdhAvoid,
	}
	defaults := &MediaPreferencesSpec{
		AudioLanguages:    []string{"default-a"},
		SubtitleLanguages: []string{"default-s"},
		Subtitles:         subtitlesAuto,
		SDH:               sdhPrefer,
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
				SDH:               sdhPrefer,
			},
		},
		{
			name:   "the Player wins when the Play sets nothing",
			player: player, defaults: defaults,
			want: resolvedPreferences{
				AudioLanguages:    []string{"player-a"},
				SubtitleLanguages: []string{"player-s"},
				Subtitles:         subtitlesOff,
				SDH:               sdhAvoid,
			},
		},
		{
			name:     "the default wins when neither the Play nor the Player sets anything",
			defaults: defaults,
			want: resolvedPreferences{
				AudioLanguages:    []string{"default-a"},
				SubtitleLanguages: []string{"default-s"},
				Subtitles:         subtitlesAuto,
				SDH:               sdhPrefer,
			},
		},
		{
			name: "a field that no tier sets resolves to nothing",
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

// An empty list that is not nil means "no preference", and it overrides
// the default's list.
func TestResolvePreferencesEmptyListOverridesALowerTier(t *testing.T) {
	play := &PlaySpec{AudioLanguages: []string{}}
	defaults := &MediaPreferencesSpec{AudioLanguages: []string{"en"}}

	got := resolvePreferences(play, nil, defaults)
	if got.AudioLanguages == nil || len(got.AudioLanguages) != 0 {
		t.Errorf("audioLanguages = %#v, want the empty list that the Play sets", got.AudioLanguages)
	}
}

func TestResolvePreferencesSkipsNilTiers(t *testing.T) {
	defaults := &MediaPreferencesSpec{AudioLanguages: []string{"en"}}

	got := resolvePreferences(nil, nil, defaults)
	want := resolvedPreferences{AudioLanguages: []string{"en"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preferences = %+v, want %+v", got, want)
	}
}

func TestResolvePreferencesTimeZoneReadsTheDefaultTierOnly(t *testing.T) {
	defaults := &MediaPreferencesSpec{TimeZone: "America/New_York"}

	got := resolvePreferences(&PlaySpec{}, &PlayerSpec{}, defaults)
	if got.TimeZone != "America/New_York" {
		t.Errorf("timeZone = %q, want %q", got.TimeZone, "America/New_York")
	}
}

func TestResolvePreferencesTimeZoneUnsetWithoutDefaults(t *testing.T) {
	got := resolvePreferences(&PlaySpec{}, &PlayerSpec{}, nil)
	if got.TimeZone != "" {
		t.Errorf("timeZone = %q, want empty", got.TimeZone)
	}
}

func fadeAfter(seconds int64) *IdlePolicy {
	return &IdlePolicy{FadeAfterSeconds: &seconds}
}

// Zero counts as set, so a Player can turn the fade off.
func TestResolveIdleFadeAfterPrefersPlayerThenDefault(t *testing.T) {
	cases := []struct {
		name     string
		player   *IdlePolicy
		defaults *IdlePolicy
		want     int64
	}{
		{name: "the Player over the default", player: fadeAfter(60), defaults: fadeAfter(900), want: 60},
		{name: "the default where the Player sets none", defaults: fadeAfter(900), want: 900},
		{name: "the built-in where neither sets one", want: defaultFadeAfterSeconds},
		{name: "an empty block on the Player", player: &IdlePolicy{}, defaults: fadeAfter(900), want: 900},
		{name: "zero on the Player", player: fadeAfter(0), defaults: fadeAfter(900), want: 0},
		{name: "zero on the default", defaults: fadeAfter(0), want: 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults, testIdleImage).FadeAfterSeconds, one.want)
		})
	}
}

func offAfter(seconds int64) *IdlePolicy {
	return &IdlePolicy{OffAfterSeconds: &seconds}
}

func TestResolveIdleOffAfterPrefersPlayerThenDefault(t *testing.T) {
	cases := []struct {
		name     string
		player   *IdlePolicy
		defaults *IdlePolicy
		want     int64
	}{
		{name: "the Player over the default", player: offAfter(120), defaults: offAfter(1800), want: 120},
		{name: "the default where the Player sets none", defaults: offAfter(1800), want: 1800},
		{name: "the built-in where neither sets one", want: defaultOffAfterSeconds},
		{name: "an empty block on the Player", player: &IdlePolicy{}, defaults: offAfter(1800), want: 1800},
		{name: "zero on the Player", player: offAfter(0), defaults: offAfter(1800), want: 0},
		{name: "zero on the default", defaults: offAfter(0), want: 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults, testIdleImage).OffAfterSeconds, one.want)
		})
	}
}

func TestResolveIdleOffModePrefersPlayerThenDefault(t *testing.T) {
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
			name:     "the default where the Player sets none",
			defaults: &IdlePolicy{OffMode: offModePower},
			want:     offModePower,
		},
		{name: "the built-in where neither sets one", want: defaultOffMode},
		{
			name:     "an empty mode on the Player",
			player:   &IdlePolicy{OffMode: ""},
			defaults: &IdlePolicy{OffMode: offModePower},
			want:     offModePower,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults, testIdleImage).OffMode, one.want)
		})
	}
}

func TestResolveIdleImagePrefersPlayerThenDefault(t *testing.T) {
	cases := []struct {
		name     string
		player   *IdlePolicy
		defaults *IdlePolicy
		want     string
	}{
		{
			name:     "the Player over the default",
			player:   &IdlePolicy{Image: "ghcr.io/liken-sh/media-browser:2026.09.01-001"},
			defaults: &IdlePolicy{Image: "ghcr.io/liken-sh/kiosk:2026.09.01-001"},
			want:     "ghcr.io/liken-sh/media-browser:2026.09.01-001",
		},
		{
			name:     "the default where the Player sets none",
			defaults: &IdlePolicy{Image: "ghcr.io/liken-sh/media-browser:2026.09.01-001"},
			want:     "ghcr.io/liken-sh/media-browser:2026.09.01-001",
		},
		{name: "the IDLE_IMAGE client where neither sets one", want: testIdleImage},
		{
			name:     "an empty image on the Player",
			player:   &IdlePolicy{Image: ""},
			defaults: &IdlePolicy{Image: "ghcr.io/liken-sh/media-browser:2026.09.01-001"},
			want:     "ghcr.io/liken-sh/media-browser:2026.09.01-001",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults, testIdleImage).Image, one.want)
		})
	}
}

func TestResolveIdleSettlesEachFieldOnItsOwn(t *testing.T) {
	player := &IdlePolicy{OffMode: offModePower}
	defaults := &IdlePolicy{
		FadeAfterSeconds: ptr(int64(900)),
		OffAfterSeconds:  ptr(int64(1800)),
		Image:            "ghcr.io/liken-sh/media-browser:2026.09.01-001",
	}

	got := resolveIdle(player, defaults, testIdleImage)

	mustMatch(t, got.FadeAfterSeconds, int64(900))
	mustMatch(t, got.OffAfterSeconds, int64(1800))
	mustMatch(t, got.OffMode, offModePower)
	mustMatch(t, got.Image, "ghcr.io/liken-sh/media-browser:2026.09.01-001")
}

func ptr[T any](value T) *T {
	return &value
}

func TestResolveIdleControllerPrefersPlayerThenDefault(t *testing.T) {
	cases := []struct {
		name     string
		player   *IdlePolicy
		defaults *IdlePolicy
		want     string
	}{
		{
			name:     "the Player over the default",
			player:   &IdlePolicy{Controller: "library.liken.sh/media-browser"},
			defaults: &IdlePolicy{Controller: idleControllerNone},
			want:     "library.liken.sh/media-browser",
		},
		{
			name:     "the default where the Player sets none",
			defaults: &IdlePolicy{Controller: "library.liken.sh/media-browser"},
			want:     "library.liken.sh/media-browser",
		},
		{name: "the built-in where neither sets one", want: idleControllerOwn},
		{
			name:     "an empty controller on the Player",
			player:   &IdlePolicy{Controller: ""},
			defaults: &IdlePolicy{Controller: idleControllerNone},
			want:     idleControllerNone,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, resolveIdle(one.player, one.defaults, testIdleImage).Controller, one.want)
		})
	}
}

func TestResolveTheNextBlocksArt(t *testing.T) {
	cases := []struct {
		name  string
		media string
		art   string
		want  string
	}{
		{
			name:  "a claim beside the media",
			media: "claim://house/library/shows/harbor/s02e04.mkv",
			art:   "claim://house/library/shows/harbor/s02e05-thumb.jpg",
			want:  "/media/1/shows/harbor/s02e05-thumb.jpg",
		},
		{
			name:  "an nfs reference beside the media",
			media: "nfs://nas.example/export/shows/s02e04.mkv",
			art:   "nfs://nas.example/export/shows/s02e05-thumb.jpg",
			want:  "/media/1/s02e05-thumb.jpg",
		},
		{
			name:  "an https reference",
			media: "claim://house/library/shows/harbor/s02e04.mkv",
			art:   "https://art.example/s02e05.jpg",
			want:  "https://art.example/s02e05.jpg",
		},
		{
			name:  "a block with no art",
			media: "claim://house/library/shows/harbor/s02e04.mkv",
			want:  "",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			resolved, err := resolvePlay("house", mediaItems(one.media), &PlayNext{Art: one.art}, frame{})
			mustSucceed(t, err)
			mustMatch(t, resolved.Next, one.want)
			mustMatch(t, len(resolved.Mounts), 1)
		})
	}
}

func TestResolveTheNextBlocksArtOnItsOwnClaim(t *testing.T) {
	resolved, err := resolvePlay("house", mediaItems("claim://house/films/movies/film.mkv"), &PlayNext{Art: "claim://house/art/shows/next.jpg"}, frame{})

	mustSucceed(t, err)

	mustMatch(t, resolved.Next, "/media/2/shows/next.jpg")
	mustMatch(t, len(resolved.Volumes), 2)
	mustMatch(t, resolved.Volumes[1].PersistentVolumeClaim.ClaimName, "art")
}

func TestResolveRefusesANextArtItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		art  string
	}{
		{name: "an unknown scheme", art: "rtsp://camera.example/still"},
		{name: "a claim in another namespace", art: "claim://studio/art/shows/next.jpg"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			_, err := resolvePlay("house", mediaItems("claim://house/films/movies/film.mkv"), &PlayNext{Art: one.art}, frame{})
			mustFail(t, err)
		})
	}
}
