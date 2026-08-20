package main

// These tests cover the two schemes the resolver knows and what each
// one costs the playback pod: an https URI costs an argument, and an
// nfs URI costs a volume, a mount, and a rewritten argument.

import (
	"reflect"
	"testing"
)

func TestResolveURIsPassesAnHTTPSURIThrough(t *testing.T) {
	resolved, err := resolveURIs([]string{"https://films.example/movies/film.mkv"})
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
func TestResolveURIsMountsTheDirectoryThatHoldsTheFile(t *testing.T) {
	resolved, err := resolveURIs([]string{"nfs://nas.example/export/dir/film.mkv"})
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

func TestResolveURIsMountsOneDirectoryOnce(t *testing.T) {
	resolved, err := resolveURIs([]string{
		"nfs://nas.example/export/dir/first.mkv",
		"nfs://nas.example/export/dir/second.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/media/1/first.mkv", "/media/1/second.mkv"}
	if !reflect.DeepEqual(resolved.Items, want) {
		t.Errorf("items = %v, want %v", resolved.Items, want)
	}
	if len(resolved.Volumes) != 1 {
		t.Fatalf("volumes = %+v, want one", resolved.Volumes)
	}
	if len(resolved.Mounts) != 1 {
		t.Fatalf("mounts = %+v, want one", resolved.Mounts)
	}
	if resolved.Volumes[0].Name != "media-1" || resolved.Mounts[0].MountPath != "/media/1" {
		t.Errorf("volume = %+v, mount = %+v", resolved.Volumes[0], resolved.Mounts[0])
	}
}

// The numbering is by first appearance, so the same playlist always
// builds the same pod.
func TestResolveURIsNumbersDirectoriesByFirstAppearance(t *testing.T) {
	resolved, err := resolveURIs([]string{
		"nfs://nas.example/export/films/film.mkv",
		"nfs://nas.example/export/shows/episode.mkv",
		"nfs://nas.example/export/films/other.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}

	items := []string{"/media/1/film.mkv", "/media/2/episode.mkv", "/media/1/other.mkv"}
	if !reflect.DeepEqual(resolved.Items, items) {
		t.Errorf("items = %v, want %v", resolved.Items, items)
	}
	volumes := []Volume{
		{Name: "media-1", NFS: &NFSVolumeSource{Server: "nas.example", Path: "/export/films", ReadOnly: true}},
		{Name: "media-2", NFS: &NFSVolumeSource{Server: "nas.example", Path: "/export/shows", ReadOnly: true}},
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
func TestResolveURIsKeepsAMixedListInSpecOrder(t *testing.T) {
	resolved, err := resolveURIs([]string{
		"https://films.example/trailer.mkv",
		"nfs://nas.example/export/films/film.mkv",
		"https://films.example/credits.mkv",
	})
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

// An unresolvable scheme's error names the two the operator does
// resolve, because the reader is writing a Play and needs the
// vocabulary.
func TestResolveURIsNamesTheSchemesItResolves(t *testing.T) {
	_, err := resolveURIs([]string{"rtsp://camera.example/stream"})
	if err == nil {
		t.Fatal("an unknown scheme resolved")
	}
	want := "the scheme rtsp:// is not one the operator resolves; it resolves https:// and nfs://"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestResolveURIsRefusesAURIItCannotMount(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{name: "no scheme at all", uri: "/export/films/film.mkv"},
		{name: "an nfs URI with no server", uri: "nfs:///export/films/film.mkv"},
		{name: "an nfs URI with no directory to mount", uri: "nfs://nas.example/film.mkv"},
		{name: "an nfs URI that names a directory", uri: "nfs://nas.example/export/films/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resolved, err := resolveURIs([]string{c.uri})
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
