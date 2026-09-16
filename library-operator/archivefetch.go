package main

// archivefetch.go is the video files of one Internet Archive item, out of the
// metadata the trailer fact already reads.

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// The path one file of one item downloads from.
const archiveDownloadPath = "/download/"

type archiveTrailerFetcher struct {
	client *archiveClient
}

func (a archiveTrailerFetcher) site() string { return trailerSiteArchive }

// Every video file the item's metadata lists, with the height and the size
// the metadata states, in the order the item holds them.
func (a archiveTrailerFetcher) files(ctx context.Context, entry trailerRow) ([]trailerFile, error) {
	item, err := a.client.item(ctx, entry.Key)
	if err != nil {
		return nil, err
	}
	files := []trailerFile{}
	for _, file := range archiveVideoFiles(item) {
		if file.Name == "" {
			continue
		}
		height, _ := strconv.Atoi(strings.TrimSpace(file.Height))
		size, _ := strconv.ParseInt(strings.TrimSpace(file.Size), 10, 64)
		files = append(files, trailerFile{
			URL:    archiveDownloadURL(a.client.base, entry.Key, file.Name),
			Height: height,
			Size:   size,
		})
	}
	return files, nil
}

// The address of one file of one item.
func archiveDownloadURL(base, identifier, name string) string {
	return base + archiveDownloadPath + identifier + "/" + url.PathEscape(name)
}
