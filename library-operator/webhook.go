package main

// webhook.go is the fast way the scanner detects a change: a small HTTP
// endpoint that accepts the webhook Radarr, Sonarr, and Jellyfin send on
// import. It reads the changed path out of the common payload shapes, maps it
// onto the volume, and rescans that one path. The slow walk is the other way,
// and it is what finds a file that arrived with no webhook.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// webhookBodyLimit bounds the payload the endpoint reads, so a webhook cannot
// hold the scanner open with an endless body.
const webhookBodyLimit = 1 << 20

// webhookRescanTimeout bounds a rescan a webhook drives, so a slow volume
// cannot hold an HTTP request open without end.
var webhookRescanTimeout = 30 * time.Second

// webhookHandler is the endpoint the *arr tools and Jellyfin post to. It reads
// the changed path, rescans it, and answers no-content. A path it cannot map to
// the volume drives a full walk, so a webhook is never worse than the slow
// timer.
func (s *scanner) webhookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(request.Body, webhookBodyLimit))
		ctx, cancel := context.WithTimeout(request.Context(), webhookRescanTimeout)
		defer cancel()

		if absolute := s.resolveWebhookPath(extractWebhookPath(body)); absolute != "" {
			s.rescan(ctx, absolute)
		} else {
			s.fullWalk(ctx)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// extractWebhookPath reads the changed path out of a webhook payload. It
// prefers a file path, then a folder path, across the shapes Radarr, Sonarr,
// and Jellyfin send, and returns the first it finds.
func extractWebhookPath(body []byte) string {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil {
		return ""
	}
	for _, field := range []struct{ object, key string }{
		{"movieFile", "path"},
		{"episodeFile", "path"},
		{"movie", "folderPath"},
		{"movie", "path"},
		{"series", "path"},
	} {
		if value := nestedString(top, field.object, field.key); value != "" {
			return value
		}
	}
	for _, key := range []string{"Path", "path"} {
		if value := topString(top, key); value != "" {
			return value
		}
	}
	return ""
}

// nestedString reads a string field from a nested object in a payload, the
// shape the *arr tools use for a file or a folder path.
func nestedString(top map[string]json.RawMessage, object, key string) string {
	raw, held := top[object]
	if !held {
		return ""
	}
	var inner map[string]json.RawMessage
	if json.Unmarshal(raw, &inner) != nil {
		return ""
	}
	return topString(inner, key)
}

// topString reads a string field from an object, and reads nothing from a
// field that is not a string.
func topString(top map[string]json.RawMessage, key string) string {
	raw, held := top[key]
	if !held {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// resolveWebhookPath maps a payload path onto the library root. The
// scanner and the enrichers resolve a SCAN_PATH through the one function
// below, so a folder the webhook named reads the same in every Job of
// its chain.
func (s *scanner) resolveWebhookPath(payloadPath string) string {
	return resolveVolumePath(s.root, payloadPath)
}

// resolveVolumePath maps one path onto the root. A relative path joins the
// root. An absolute path is the media server's own, whose prefix no
// container can know, so the resolver takes the longest suffix of it that
// exists under the root. A path that maps to nothing returns empty, and the
// caller covers the whole library.
func resolveVolumePath(root, payloadPath string) string {
	payloadPath = strings.TrimSpace(payloadPath)
	if payloadPath == "" {
		return ""
	}
	cleaned := filepath.Clean(payloadPath)
	if !filepath.IsAbs(cleaned) {
		candidate := filepath.Join(root, cleaned)
		if pathExists(candidate) {
			return candidate
		}
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	for i := range parts {
		candidate := filepath.Join(append([]string{root}, parts[i:]...)...)
		if pathExists(candidate) {
			return candidate
		}
	}
	return ""
}
