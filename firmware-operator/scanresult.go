package main

// The scan, rehearsal, and apply programs each print their result as
// one line of JSON at the end of the pod's log. fwupd writes its own
// lines to the same log, so the operator uses the last line that parses
// as a JSON object with a result. A program that crashed printed no
// such line, and the caller records the attempt as failed with the
// log's last line as the message.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// scanResult is the JSON line that the scan program prints.
type scanResult struct {
	Result  string       `json:"result"`
	Message string       `json:"message"`
	File    string       `json:"file"`
	Devices []scanDevice `json:"devices"`
}

// scanDevice is one device that fwupd reports it can update, with the
// releases that the catalog offers for it.
type scanDevice struct {
	Name     string           `json:"name"`
	DeviceID string           `json:"deviceId"`
	Plugin   string           `json:"plugin"`
	GUIDs    []string         `json:"guids"`
	Version  string           `json:"version"`
	Vendor   string           `json:"vendor"`
	Releases []offeredRelease `json:"releases"`
}

// offeredRelease is one release that a catalog offers for a device, as
// the scan reports it and a discovered Firmware records it.
type offeredRelease struct {
	Version string   `json:"version"`
	ID      string   `json:"id,omitempty"`
	Urgency string   `json:"urgency,omitempty"`
	Cabinet *cabinet `json:"cabinet,omitempty"`
}

// cabinet is the .cab file of a release. An approval matches a release
// by this file's SHA-256.
type cabinet struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

// podLogLines is how many lines the operator reads from the end of a
// pod's log. The result line is at the end, so the operator does not
// need the many lines that fwupd writes before it.
const podLogLines = 50

// readPodLog reads the last podLogLines lines of a finished pod's log.
func (o *operator) readPodLog(p pod, container string) (string, error) {
	var body []byte
	path := fmt.Sprintf("%s/log?container=%s&tailLines=%d", podPath(p.Metadata.Namespace, p.Metadata.Name), container, podLogLines)
	if err := o.client.RequestJSON(http.MethodGet, path, nil, &body); err != nil {
		return "", err
	}
	return string(body), nil
}

// parseScanLog is parseResultLine for a scan pod's log.
func parseScanLog(log string) (scanResult, string, bool) {
	return parseResultLine[scanResult](log, func(r scanResult) string { return r.Result })
}

// parseResultLine returns the last line of a log that parses as a JSON
// object whose result is not empty. When there is no such line, it
// returns false and the log's last line.
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
