// The reader for a Cobertura report, the file `cargo llvm-cov
// --cobertura` writes.
package main

import (
	"encoding/xml"
	"path/filepath"
)

// The parts of the format this program reads. A class is one source
// file. Its lines element holds every line the compiler
// instrumented, and the lines under a method are the same lines
// again, one method at a time, so only the class's own list counts.
//
// The report says nothing about the language that wrote it, so the
// page names such an input after the file or after the -label the
// command line gives.
type coberturaReport struct {
	XMLName xml.Name         `xml:"coverage"`
	Covered int              `xml:"lines-covered,attr"`
	Valid   int              `xml:"lines-valid,attr"`
	Sources []string         `xml:"sources>source"`
	Classes []coberturaClass `xml:"packages>package>classes>class"`
}

type coberturaClass struct {
	Filename string          `xml:"filename,attr"`
	Lines    []coberturaLine `xml:"lines>line"`
}

type coberturaLine struct {
	Number int `xml:"number,attr"`
	Hits   int `xml:"hits,attr"`
}

// readCobertura counts lines, which is what the format holds. The
// totals come from the root element, because that is the number the
// tool that wrote the report stands behind.
func readCobertura(data []byte, label, root string) (*Source, error) {
	var report coberturaReport
	if err := xml.Unmarshal(data, &report); err != nil {
		return nil, err
	}

	source := &Source{
		Label:   label,
		Unit:    "lines",
		Covered: report.Covered,
		Total:   report.Valid,
	}
	byName := map[string]*File{}
	for _, class := range report.Classes {
		file := byName[class.Filename]
		if file == nil {
			file = &File{
				Name:  class.Filename,
				Marks: map[int]Mark{},
				Text:  readText(report.candidates(class.Filename, root)...),
			}
			byName[class.Filename] = file
		}
		for _, line := range class.Lines {
			mark := Uncovered
			if line.Hits > 0 {
				mark = Covered
			}
			file.mark(line.Number, mark)
		}
	}

	// A file's own numbers come from its lines, counted once each,
	// because a report may name one file in more than one class.
	for _, file := range byName {
		file.Total = len(file.Marks)
		for _, mark := range file.Marks {
			if mark == Covered {
				file.Covered++
			}
		}
	}

	source.Files = sortedFiles(byName)
	return source, nil
}

// candidates lists where one class's file may stand, nearest first.
// The filenames are relative to the source element, which holds the
// path of the tree the tests ran in. That machine is often not this
// one, so the root wins wherever it holds the file, and the source
// element is the fallback.
func (r coberturaReport) candidates(filename, root string) []string {
	paths := []string{filepath.Join(root, filename)}
	for _, source := range r.Sources {
		paths = append(paths, filepath.Join(source, filename))
	}
	return paths
}
