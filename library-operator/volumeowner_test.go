package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A title's folder that another group owns, the way a folder Radarr or Sonarr
// made is owned by the uid and gid they run as. A user who is not root can
// give a directory to any group they belong to, so the test needs a second
// group and skips where the user has one alone.
func foreignFolder(t *testing.T) (string, int) {
	t.Helper()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	other := -1
	for _, group := range groups {
		if group != os.Getegid() {
			other = group
			break
		}
	}
	if other < 0 {
		t.Skip("the user belongs to one group, so no folder can belong to another")
	}
	folder := filepath.Join(t.TempDir(), "Film [1999]")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(folder, -1, other); err != nil {
		t.Fatal(err)
	}
	return folder, other
}

func groupOf(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return int(info.Sys().(*syscall.Stat_t).Gid)
}

// Every file and directory the operator lands in a title's folder takes the
// folder's owner, so the tool that owns the folder can rewrite and delete it.
// Radarr's import writes movie.nfo again after an upgrade, and a file it may
// not write fails the import.
func TestEveryWriteTakesTheOwnerOfTheFolderItLandsIn(t *testing.T) {
	for _, test := range []struct {
		name   string
		write  func(t *testing.T, writer *volumeWriter, folder string)
		landed []string
	}{
		{
			name: "a written file",
			write: func(t *testing.T, writer *volumeWriter, folder string) {
				if err := writer.write(filepath.Join(folder, "movie.nfo"), []byte("<movie/>")); err != nil {
					t.Fatal(err)
				}
			},
			landed: []string{"movie.nfo"},
		},
		{
			name: "an updated file",
			write: func(t *testing.T, writer *volumeWriter, folder string) {
				if _, err := writer.update(filepath.Join(folder, "movie.nfo"), func([]byte) ([]byte, error) {
					return []byte("<movie/>"), nil
				}); err != nil {
					t.Fatal(err)
				}
			},
			landed: []string{"movie.nfo"},
		},
		{
			name: "a file created once",
			write: func(t *testing.T, writer *volumeWriter, folder string) {
				if _, err := writer.createOnce(filepath.Join(folder, "poster.jpg"), []byte("art")); err != nil {
					t.Fatal(err)
				}
			},
			landed: []string{"poster.jpg"},
		},
		{
			name: "a ledger in a new .liken directory",
			write: func(t *testing.T, writer *volumeWriter, folder string) {
				if err := writer.writeInto(filepath.Join(folder, ".liken"), "overview.yaml", []byte("items: []")); err != nil {
					t.Fatal(err)
				}
			},
			landed: []string{".liken", ".liken/overview.yaml"},
		},
		{
			name: "a staged tree",
			write: func(t *testing.T, writer *volumeWriter, folder string) {
				target := filepath.Join(folder, "Film [1999].trickplay")
				staging, err := writer.stageTree(target)
				if err != nil {
					t.Fatal(err)
				}
				tiles := filepath.Join(staging, "320 - 10x10")
				if err := os.MkdirAll(tiles, volumeDirectoryPerm); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(tiles, "0.jpg"), "tile")
				if _, err := writer.createTree(target); err != nil {
					t.Fatal(err)
				}
			},
			landed: []string{"Film [1999].trickplay", "Film [1999].trickplay/320 - 10x10", "Film [1999].trickplay/320 - 10x10/0.jpg"},
		},
		{
			name: "a pulled file in a new trailers directory",
			write: func(t *testing.T, writer *volumeWriter, folder string) {
				directory := filepath.Join(folder, trailersFolderName)
				if err := writer.makeDirectory(directory); err != nil {
					t.Fatal(err)
				}
				pulled := writer.hiddenTemporary(directory, trailerPullMark)
				writeFile(t, pulled, "video")
				if _, err := writer.createOnceFrom(pulled, filepath.Join(directory, "Trailer.mp4")); err != nil {
					t.Fatal(err)
				}
			},
			landed: []string{trailersFolderName, trailersFolderName + "/Trailer.mp4"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			folder, group := foreignFolder(t)

			test.write(t, newVolumeWriter("movies-enrich"), folder)

			for _, name := range test.landed {
				if got := groupOf(t, filepath.Join(folder, name)); got != group {
					t.Errorf("%s belongs to group %d, want the folder's %d", name, got, group)
				}
			}
		})
	}
}

// A run that may not change an owner, such as one as a user other than root
// or on a share that maps root to another user, writes each file as itself,
// and the write still lands.
func TestAFileTheRunMayNotHandOverKeepsItsOwner(t *testing.T) {
	for _, test := range []struct {
		name      string
		directory string
	}{
		{name: "a folder another user owns", directory: "/"},
		{name: "a folder that is gone", directory: filepath.Join(t.TempDir(), "gone")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "movie.nfo")
			writeFile(t, path, "<movie/>")

			adoptOwner(path, test.directory)

			if got := groupOf(t, path); got != os.Getegid() {
				t.Errorf("movie.nfo belongs to group %d, want the run's own %d", got, os.Getegid())
			}
		})
	}
}

// A directory under a file can never be made, and the make reports why.
func TestADirectoryUnderAFileIsRefused(t *testing.T) {
	file := filepath.Join(t.TempDir(), "movie.nfo")
	writeFile(t, file, "<movie/>")

	if err := newVolumeWriter("movies-enrich").makeDirectory(filepath.Join(file, ".liken")); err == nil {
		t.Error("made a directory under a file")
	}
}
