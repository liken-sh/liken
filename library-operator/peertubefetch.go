package main

// peertubefetch.go is the video files of one PeerTube video: the instance's
// own files where it states any, and the files of its streaming playlist
// where it states none.

import (
	"context"
)

// The path one video answers on.
const peertubeVideoPath = "/api/v1/videos/"

// One video as an instance answers it, in the fields the pull reads.
type peertubeVideoDetail struct {
	UUID               string                      `json:"uuid"`
	Name               string                      `json:"name"`
	Duration           int                         `json:"duration"`
	Files              []peertubeVideoFile         `json:"files"`
	StreamingPlaylists []peertubeStreamingPlaylist `json:"streamingPlaylists"`
}

// One file of a video, by height, size, and the address that answers the
// whole file to a plain GET.
type peertubeVideoFile struct {
	Resolution      peertubeResolution `json:"resolution"`
	Size            int64              `json:"size"`
	FileDownloadURL string             `json:"fileDownloadUrl"`
}

// The resolution an instance states for the audio track of a video.
const peertubeAudioResolution = 0

// The height of one file, which the instance states as the id.
type peertubeResolution struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// One HLS playlist of a video, with the fragmented files behind it.
type peertubeStreamingPlaylist struct {
	PlaylistURL string              `json:"playlistUrl"`
	Files       []peertubeVideoFile `json:"files"`
}

func (c *peertubeClient) video(ctx context.Context, uuid string) (peertubeVideoDetail, error) {
	var answer peertubeVideoDetail
	if err := c.get(ctx, peertubeVideoPath+uuid, nil, &answer); err != nil {
		return peertubeVideoDetail{}, err
	}
	return answer, nil
}

type peertubeTrailerFetcher struct {
	client *peertubeClient
}

func (p peertubeTrailerFetcher) site() string { return trailerSitePeerTube }

func (p peertubeTrailerFetcher) files(ctx context.Context, entry trailerRow) ([]trailerFile, error) {
	video, err := p.client.video(ctx, entry.Key)
	if err != nil {
		return nil, err
	}
	return peertubeTrailerFiles(video), nil
}

// Many instances answer an empty files list, so the streaming playlist's own
// files are what the pull reads there.
func peertubeTrailerFiles(video peertubeVideoDetail) []trailerFile {
	if files := peertubeFilesOf(video.Files); len(files) > 0 {
		return files
	}
	files := []trailerFile{}
	for _, playlist := range video.StreamingPlaylists {
		files = append(files, peertubeFilesOf(playlist.Files)...)
	}
	return files
}

// A file with no download address is one the pull cannot take, and a file of
// resolution 0 is the audio of the video and holds no picture.
func peertubeFilesOf(files []peertubeVideoFile) []trailerFile {
	held := []trailerFile{}
	for _, file := range files {
		if file.FileDownloadURL == "" || file.Resolution.ID == peertubeAudioResolution {
			continue
		}
		held = append(held, trailerFile{
			URL: file.FileDownloadURL, Height: file.Resolution.ID, Size: file.Size,
		})
	}
	return held
}
