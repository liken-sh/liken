package main

// The scan program writes its answer as one line of JSON, the last
// line of its log. fwupd writes its own lines to the same log, so the
// operator takes the last line that parses as a JSON object with a
// result key. A scan that crashed wrote no such line, and its result is
// Failed with the last line it wrote.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// scanResult is the line the scan program writes.
type scanResult struct {
	Result  string       `json:"result"`
	Message string       `json:"message"`
	File    string       `json:"file"`
	Devices []scanDevice `json:"devices"`
}

// scanDevice is one device that fwupd reported as updatable, with the
// releases the catalog offers for it.
type scanDevice struct {
	Name     string           `json:"name"`
	DeviceID string           `json:"deviceId"`
	Plugin   string           `json:"plugin"`
	GUIDs    []string         `json:"guids"`
	Version  string           `json:"version"`
	Vendor   string           `json:"vendor"`
	Releases []offeredRelease `json:"releases"`
}

// offeredRelease is one release a catalog offers for a device, as the
// scan reports it and as a discovered Firmware records it.
type offeredRelease struct {
	Version string   `json:"version"`
	ID      string   `json:"id,omitempty"`
	Urgency string   `json:"urgency,omitempty"`
	Cabinet *cabinet `json:"cabinet,omitempty"`
}

// cabinet is the file that carries a release. Plan 04 pins an approval
// to its SHA-256.
type cabinet struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

// scanLogLines bounds the read of a scan pod's log to its end, where
// the result line is. fwupd can write many lines before it.
const scanLogLines = 50

// readPodLog reads the end of a finished scan or rehearsal pod's log.
func (o *operator) readPodLog(p pod, container string) (string, error) {
	var body []byte
	path := fmt.Sprintf("%s/log?container=%s&tailLines=%d", podPath(p.Metadata.Namespace, p.Metadata.Name), container, scanLogLines)
	if err := o.client.RequestJSON(http.MethodGet, path, nil, &body); err != nil {
		return "", err
	}
	return string(body), nil
}

// parseScanLog answers the last result line of a scan's log, or false
// and the last line of the log when there is none.
func parseScanLog(log string) (scanResult, string, bool) {
	return parseResultLine[scanResult](log, func(r scanResult) string { return r.Result })
}

// parseResultLine answers the last line of a log that parses as a JSON
// object with a result, or false and the last line of the log when
// there is none. A rehearsal writes its answer the same way a scan
// does.
func parseResultLine[T any](log string, result func(T) string) (T, string, bool) {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		var parsed T
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &parsed) != nil || result(parsed) == "" {
			continue
		}
		return parsed, "", true
	}
	var none T
	return none, lastLine(log), false
}
