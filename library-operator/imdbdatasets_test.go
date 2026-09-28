package main

// what these tests read: the reader of a decompressed dataset file skips the
// header, reads IMDb's null mark as empty, takes a line longer than its
// buffer, and refuses a line longer than a mebibyte.

import (
	"slices"
	"strings"
	"testing"
)

func TestTheDatasetReaderReadsEachRowsCells(t *testing.T) {
	long := strings.Repeat("x", 100*1024)
	cases := []struct {
		name string
		file string
		want []string
	}{
		{name: "a null cell", file: "tconst\tseason\ntt1\t\\N\n", want: []string{"tt1|"}},
		{name: "no final newline", file: "tconst\tseason\ntt1\t2", want: []string{"tt1|2"}},
		{name: "a line longer than the buffer", file: "tconst\tseason\ntt1\t" + long + "\n",
			want: []string{"tt1|" + long}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			var rows []string
			err := readDatasetRows(strings.NewReader(one.file), func(cells [][]byte) {
				rows = append(rows, datasetCell(cells, 0)+"|"+datasetCell(cells, 1))
			})
			if err != nil || !slices.Equal(rows, one.want) {
				t.Errorf("rows = %v, %v, want %v", rows, err, one.want)
			}
		})
	}
}

func TestTheDatasetReaderRefusesALineOverAMebibyte(t *testing.T) {
	file := "tconst\n" + strings.Repeat("x", datasetMaxLine+1) + "\n"

	if err := readDatasetRows(strings.NewReader(file), func([][]byte) {}); err == nil {
		t.Error("read = nil, want an error")
	}
}
