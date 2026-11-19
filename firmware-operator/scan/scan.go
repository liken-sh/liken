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

// config is the scan's input. The operator sets each field as an
// environment variable on the scan container.
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

// scanner holds the directories, the HTTP client, and the fwupdtool
// command of a scan, so that a test can replace each of them.
type scanner struct {
	cfg    config
	client *http.Client
	// firmwareHost is the machine's /sys/firmware, mounted read-only.
	// fwupd reads firmwareCopy instead, through FWUPD_SYSFSFWDIR,
	// because its uefi_capsule plugin lists devices only when the EFI
	// variable store is a writable mount point. The pod mounts an
	// in-memory volume as the copy's variable store, so the machine's
	// own store is never written.
	firmwareHost string
	firmwareCopy string
	fwupdtool    fwupdtool
	remotesDir   string // fwupd's remote configuration, /etc/fwupd/remotes.d
	workDir      string // where the catalog files are saved for the refresh
	trustDir     string // the catalog's certificates, when it has spec.trust (trust.go)
	pkiDir       string // the keys fwupd trusts for metadata
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// scan stops at the first step that fails. It returns a failure as a
// Result and not as an error, because the operator reads every outcome
// from the Result in the pod's log.
func (s scanner) scan(ctx context.Context) Result {
	cfg := s.cfg
	if err := copyScanFirmware(s.firmwareHost, s.firmwareCopy); err != nil {
		return failed(cfg.file, "copying the firmware tables: %v", err)
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

// loadCatalog downloads the jcat and the catalog file, configures the
// FirmwareCatalog as a fwupd remote, and refreshes fwupd from the saved
// files. The file's SHA-256 must equal CATALOG_SHA256, and the jcat's
// digest when the jcat has one. It returns nil on success. Otherwise it
// returns the Result that ends the scan: CatalogMoved when the jcat
// lists a different file than CATALOG_FILE, or Failed.
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
			Message: fmt.Sprintf("the jcat now lists %s", item.file),
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
	if got != cfg.sha256 || got != item.sha256 {
		return end(failed(cfg.file, "the SHA-256 of %s is %s, and the operator expects %s", item.file, got, cfg.sha256))
	}

	if err := configureRemotes(s.remotesDir, cfg.name, cfg.url); err != nil {
		return end(failed(cfg.file, "writing the fwupd remote: %v", err))
	}
	if err := installTrust(s.trustDir, s.pkiDir, cfg.name); err != nil {
		return end(failed(cfg.file, "installing the catalog's certificates from spec.trust: %v", err))
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

// save writes the catalog and its jcat into workDir, under the names
// <file> and <file>.jcat that fwupd pairs them by.
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

// copyScanFirmware copies the machine's firmware tables for fwupd. A
// machine that booted in BIOS mode has no efi/efivars, so nothing is
// copied and fwupd correctly finds no UEFI devices.
func copyScanFirmware(host, dest string) error {
	if _, err := os.Stat(filepath.Join(host, "efi", "efivars")); err != nil {
		return nil
	}
	return copyFirmware(host, dest)
}
