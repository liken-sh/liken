package main

// imdbdatasets.go names the IMDb dataset files and the facts that read them,
// and reads one file as rows. IMDb publishes no API. It publishes a set of
// gzipped TSV files at datasets.imdbws.com and replaces each file every day.
// The files are for personal and non-commercial use, so no image carries
// them: the operator's check reads their headers, and an enricher container
// downloads the files it needs.

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"slices"
)

// The address IMDb serves the datasets from. A test replaces it through the
// operator's providerBases and the container's IMDB_ENDPOINT.
const imdbDatasetsBase = "https://datasets.imdbws.com"

// The dataset files a fact reads. Each name is the file's name at IMDb with
// no .tsv.gz suffix.
const (
	datasetTitleRatings = "title.ratings"
	datasetTitleEpisode = "title.episode"
)

// The files each served fact reads, in the order a run reads them. The rating
// reads title.episode first, because an episode with no IMDb id takes its id
// from that file, and the read of title.ratings keeps only the ids it holds.
var datasetsOfFact = map[string][]string{
	factRatingIMDb: {datasetTitleEpisode, datasetTitleRatings},
}

// Every file the given facts read, in the order of the table, with no file
// named twice.
func datasetsFor(facts []string) []string {
	var files []string
	for _, fact := range facts {
		for _, file := range datasetsOfFact[fact] {
			if !slices.Contains(files, file) {
				files = append(files, file)
			}
		}
	}
	return files
}

// The address of one file.
func datasetURL(base, name string) string {
	return base + "/" + name + ".tsv.gz"
}

// The value IMDb writes in a cell that holds nothing.
const datasetNull = `\N`

// The longest line the reader accepts. The longest row of the four files
// the plan reads is a few kilobytes, so a longer line is a file this reader
// does not understand.
const datasetMaxLine = 1 << 20

// readDatasetRows reads one decompressed file line by line and hands each
// row's cells to keep. The first line is the header, and the reader skips it.
// The reader holds one line at a time and not the file, so a file of
// millions of rows reads in the memory of one buffer. The cells slice and its
// strings are valid only until keep returns, so keep copies what it holds.
func readDatasetRows(r io.Reader, keep func(cells [][]byte)) error {
	lines := bufio.NewReaderSize(r, 64*1024)
	header := true
	var cells [][]byte
	for {
		line, err := lines.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			line, err = readLongLine(lines, line)
		}
		if len(line) > 0 && !header {
			cells = splitCells(bytes.TrimRight(line, "\r\n"), cells[:0])
			keep(cells)
		}
		header = false
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// A line longer than the buffer, read to its end in a copy, up to the limit.
func readLongLine(lines *bufio.Reader, start []byte) ([]byte, error) {
	line := slices.Clone(start)
	for len(line) < datasetMaxLine {
		more, err := lines.ReadSlice('\n')
		line = append(line, more...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
	return nil, errors.New("a dataset line is longer than one mebibyte")
}

// The tab-separated cells of one line, reusing the slice the caller passes.
func splitCells(line []byte, cells [][]byte) [][]byte {
	for {
		cell, rest, found := bytes.Cut(line, []byte{'\t'})
		cells = append(cells, cell)
		if !found {
			return cells
		}
		line = rest
	}
}

// One cell as text, with IMDb's null mark read as empty.
func datasetCell(cells [][]byte, index int) string {
	if index >= len(cells) || string(cells[index]) == datasetNull {
		return ""
	}
	return string(cells[index])
}
