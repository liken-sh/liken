package capture

import (
	"bytes"
	"os"
	"path/filepath"
)

// WriteTranscripts writes each reply of a simulator to its own file in
// dir, named for its phase.
func WriteTranscripts(dir string, replies []Reply) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, reply := range replies {
		if err := os.WriteFile(filepath.Join(dir, reply.Phase+".xml"), reply.Data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// WriteCapture writes the capture of one bare driver to dir, and
// returns the number of properties it holds. Every capture but the one
// of DefaultDevice leaves out the properties that dir's capture of
// DefaultDevice holds, so that one is written first.
func WriteCapture(dir string, bare Bare, version, device string, replies []Reply) (int, error) {
	defs, err := Definitions(replies)
	if err != nil {
		return 0, err
	}
	exclude := map[string]bool{}
	if bare.Name != DefaultDevice {
		if exclude, err = defaultDeviceProperties(dir); err != nil {
			return 0, err
		}
	}
	header := Header{Driver: bare.Driver, Device: device, INDI: version, Interface: Interface(defs)}
	var out bytes.Buffer
	if err := Write(&out, header, defs, exclude); err != nil {
		return 0, err
	}
	kept := 0
	for _, d := range defs {
		if !exclude[d.Name] {
			kept++
		}
	}
	return kept, os.WriteFile(filepath.Join(dir, bare.Name+".xml"), out.Bytes(), 0o644)
}

func defaultDeviceProperties(dir string) (map[string]bool, error) {
	file, err := os.Open(filepath.Join(dir, DefaultDevice+".xml"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	c, err := Read(file)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, p := range c.Phases {
		for _, d := range p.Properties {
			names[d.Name] = true
		}
	}
	return names, nil
}
