// This file reads a Cobertura report, the file that
// `cargo llvm-cov --cobertura` writes.

package main

import (
	"encoding/xml"
	"path/filepath"
)

// coberturaReport holds the parts of the Cobertura format that this
// program reads. A class is one source file, and its lines element
// lists every instrumented line. Each method lists its own lines
// again, so the program reads only the class's list.
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

// readCobertura reads a Cobertura report, which counts lines. The
// report's totals come from the lines-covered and lines-valid
// attributes of the root element, as the tool wrote them.
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

	// A file's totals come from its marked lines, so a file that
	// appears in more than one class counts each line once.
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

// candidates returns the paths to try for a class's source file, in
// order. A filename is relative to the source elements, which give the
// path of the tree on the machine that ran the tests. That is often
// another machine, so the path under root comes first.
func (r coberturaReport) candidates(filename, root string) []string {
	paths := []string{filepath.Join(root, filename)}
	for _, source := range r.Sources {
		paths = append(paths, filepath.Join(source, filename))
	}
	return paths
}
