package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// config is the scan's input, which the operator sets as environment
// variables on the pod's container.
type config struct {
	name   string // CATALOG_NAME, the FirmwareCatalog object's name
	url    string // CATALOG_URL, the unversioned catalog URL
	file   string // CATALOG_FILE, the versioned file the operator expects
	sha256 string // CATALOG_SHA256, the digest of that file
}

func configFromEnv(getenv func(string) string) config {
	return config{
		name:   getenv("CATALOG_NAME"),
		url:    getenv("CATALOG_URL"),
		file:   getenv("CATALOG_FILE"),
		sha256: getenv("CATALOG_SHA256"),
	}
}

// scanner holds what a scan touches outside the process, so that a test
// substitutes each one.
type scanner struct {
	cfg        config
	client     *http.Client
	mounter    mounter
	fwupdtool  fwupdtool
	remotesDir string // fwupd's remote configuration, /etc/fwupd/remotes.d
	workDir    string // where the catalog files are saved for the refresh
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// scan runs the steps in order and stops at the first that fails. It
// never returns an error: every outcome, including a failure, is a
// Result for the operator to read.
func (s scanner) scan(ctx context.Context) Result {
	cfg := s.cfg
	if err := s.mounter.ensureEFIVars(); err != nil {
		return failed(cfg.file, "%v", err)
	}
	if end := s.loadCatalog(ctx); end != nil {
		return *end
	}
	devices, err := discover(ctx, s.fwupdtool)
	if err != nil {
		return failed(cfg.file, "%v", err)
	}
	return Result{Result: resultScanned, File: cfg.file, Message: summary(devices), Devices: devices}
}

// loadCatalog downloads the catalog file that the operator named,
// checks it against the jcat and the operator's SHA-256, makes its
// FirmwareCatalog fwupd's only remote, and refreshes fwupd from it. A
// result other than nil ends the run: the jcat names another file, or
// a step failed.
func (s scanner) loadCatalog(ctx context.Context) *Result {
	cfg := s.cfg
	end := func(r Result) *Result { return &r }
	jcatURL := cfg.url + ".jcat"
	jcatData, err := get(ctx, s.client, jcatURL)
	if err != nil {
		return end(failed(cfg.file, "downloading the jcat: %v", err))
	}
	item, err := parseJcat(jcatData)
	if err != nil {
		return end(failed(cfg.file, "reading the jcat: %v", err))
	}
	if item.file != cfg.file {
		return end(Result{
			Result: resultCatalogMoved, File: cfg.file,
			Message: fmt.Sprintf("the jcat now names %s", item.file),
		})
	}

	fileURL, err := siblingURL(cfg.url, item.file)
	if err != nil {
		return end(failed(cfg.file, "building the URL of %s: %v", item.file, err))
	}
	fileData, err := get(ctx, s.client, fileURL)
	if err != nil {
		return end(failed(cfg.file, "downloading %s: %v", item.file, err))
	}
	got := sum(fileData)
	if got != cfg.sha256 || (item.sha256 != "" && got != item.sha256) {
		return end(failed(cfg.file, "the SHA-256 of %s is %s, and the operator expects %s", item.file, got, cfg.sha256))
	}

	if err := configureRemotes(s.remotesDir, cfg.name, cfg.url); err != nil {
		return end(failed(cfg.file, "writing the fwupd remote: %v", err))
	}
	paths, err := s.save(item.file, fileData, jcatData)
	if err != nil {
		return end(failed(cfg.file, "saving the catalog: %v", err))
	}
	if _, stderr, err := s.fwupdtool(ctx, "refresh", paths[0], paths[1], remoteID(cfg.name)); err != nil {
		return end(failed(cfg.file, "fwupdtool refresh: %s", lastLine(stderr)))
	}
	return nil
}

// save writes the catalog and its jcat side by side under the names
// that fwupd pairs them by: the jcat is the catalog's name with .jcat
// appended.
func (s scanner) save(file string, catalog, jcat []byte) ([]string, error) {
	if err := os.MkdirAll(s.workDir, 0o755); err != nil {
		return nil, err
	}
	paths := []string{filepath.Join(s.workDir, file), filepath.Join(s.workDir, file+".jcat")}
	if err := os.WriteFile(paths[0], catalog, 0o644); err != nil {
		return nil, err
	}
	return paths, os.WriteFile(paths[1], jcat, 0o644)
}
