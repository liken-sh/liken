package main

// folderfiles.go reads one directory into the file rows a title carries:
// one stat per file, the class files.go reads off its name, the item it
// links to, and the probe record the folder's ledger holds for it.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// folderFiles is one directory the walk reads for the files a title carries.
// The walk reads a title folder, a season folder under a series, and an extras
// folder under a title folder, and it descends no further.
type folderFiles struct {
	root    string
	dir     string
	library string
	place   filePlace
	// item names the item a file in this directory links to. A season folder
	// answers with the episode whose own name the file starts with; every
	// other place answers with one id.
	// The answer is a list, because a file beside a double-episode video
	// belongs to both of its episodes.
	item func(name string) []string
	// held is the names the item walk already wrote a row for, so this pass
	// adds no second row for a video that carries its sidecar's attributes.
	held map[string]bool
	// The probe records of the ledger that names this directory's files. The
	// caller reads the ledger once and hands it to every read of the folder.
	probes folderProbes
}

// The stream rows come back beside the file rows, one set per file a probe
// has read.
func (f folderFiles) read() ([]fileRow, []streamRow, []string, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, nil, nil, err
	}
	var rows []fileRow
	var streams []streamRow
	var subdirectories []string
	for _, entry := range entries {
		name := entry.Name()
		if skipName(name) {
			continue
		}
		if entry.IsDir() {
			if strings.EqualFold(filepath.Ext(name), trickplayExtension) {
				row, _, err := f.row(name, fileClass{Type: fileTypeTrickplay, Role: fileRoleTiles})
				if err != nil {
					return rows, streams, subdirectories, err
				}
				rows = append(rows, row)
				continue
			}
			subdirectories = append(subdirectories, name)
			continue
		}
		if f.held[name] {
			continue
		}
		row, rowStreams, err := f.row(name, classifyFile(name, f.place))
		if err != nil {
			return rows, streams, subdirectories, err
		}
		rows = append(rows, row)
		streams = append(streams, rowStreams...)
	}
	return rows, streams, subdirectories, nil
}

// A video or an audio file with a record in the folder's probe ledger takes
// its technical columns and its stream rows from that record. A video with
// no record reads the sidecar beside it, as it did before the ledger
// existed.
func (f folderFiles) row(name string, class fileClass) (fileRow, []streamRow, error) {
	absolute := filepath.Join(f.dir, name)
	size, modified, err := statFile(absolute)
	if err != nil {
		return fileRow{}, nil, err
	}
	row := fileRow{
		Path:      relativePath(f.root, absolute),
		Library:   f.library,
		Container: containerFromExtension(name),
		SizeBytes: size,
		Modified:  modified,
		Type:      class.Type,
		Role:      class.Role,
		Language:  class.Language,
		Present:   true,
	}
	if class.Type == fileTypeVideo {
		stream, err := streamBeside(f.dir, name)
		if err != nil {
			return fileRow{}, nil, err
		}
		row.Container, row.VideoCodec, row.AudioCodec, row.Width, row.Height, row.DurationMs =
			fileAttributes(name, stream)
		row.Trickplay = trickplayFor(f.root, f.dir, name)
	}
	var streams []streamRow
	if class.Type == fileTypeVideo || class.Type == fileTypeAudio {
		_, entry := likenFolderFor(f.place.kind, absolute)
		streams = f.probes.fill(&row, entry)
	}
	if items := f.item(name); len(items) > 0 {
		row.Items = items
	}
	return row, streams, nil
}

// The probe writes one video's stream details into the sidecar beside it,
// under the same name with the .nfo extension. The sidecar is read for its
// streamdetails alone, so a movie sidecar and an episode sidecar both answer
// here. A video with no sidecar is not an error and reads its name instead. A
// sidecar the scanner cannot read is an error, because a row from the name
// would replace the details the volume holds and the prune would act on it.
func streamBeside(dir, file string) (*streamInfo, error) {
	data, err := os.ReadFile(sidecarBeside(filepath.Join(dir, file)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stream, err := parseStreamNFO(data)
	if err != nil || !stream.present() {
		return nil, nil
	}
	return &stream, nil
}

// constantItem answers with one item id for every file in a directory, the
// resolver a movie title folder and a series folder both use.
func constantItem(item string) func(string) []string {
	return func(string) []string { return []string{item} }
}

// episodeItem answers with the episode whose own file name a file's name starts
// with, and with the series where it matches none, which is where a season
// poster lands. The longest match wins, so an episode does not take a file that
// belongs to another episode whose name it is a prefix of.
// A video of two episodes carries both ids, so its sidecar, its subtitle, and
// its still link to the same episodes the video does.
func episodeItem(episodes map[string][]string, series string) func(string) []string {
	return func(name string) []string {
		base := stripAnyExtension(name)
		longest, items := "", []string{series}
		for episodeBase, ids := range episodes {
			if len(episodeBase) > len(longest) && strings.HasPrefix(base, episodeBase) {
				longest, items = episodeBase, ids
			}
		}
		return items
	}
}

// statFile reads a path's size in bytes and the time it was last written, in
// Unix seconds, from one stat. A stat that fails is an error the caller
// folds into the walk's incomplete mark, because a size and a time of
// zero would otherwise land in the catalog as facts.
func statFile(path string) (int64, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	return info.Size(), info.ModTime().Unix(), nil
}
