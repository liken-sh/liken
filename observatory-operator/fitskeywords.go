package main

// The Observatory, the Telescope, the Camera, and the Reservation can
// each declare FITS keywords in spec.fitsKeywords. Configure writes the
// merged list to each imaging camera's FITS_HEADER property, and the
// driver writes the keywords into every frame it saves, including the
// frames that a desktop client takes.
//
// A driver adds each keyword it receives to every later frame until
// its process exits or a client clears the keywords. So the PowerOn
// step records each imaging camera's merged list in status.session, and
// Configure clears the camera's keywords and writes the record, not the
// specs. An edit to a spec takes effect at the next session, and every
// frame of a session has the same keywords. A driver that restarts with
// its pod has no keywords, and the runner configures it again
// (steady.go), which writes the record again.

import (
	"context"
	"fmt"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// fitsKeywordsOf returns the keywords of each imaging camera among a
// telescope's devices, keyed by device key, merged from the
// Observatory, the Telescope, the camera, and the reservation. A guide
// camera sends its frames to PHD2 and saves no file, so it has no
// entry, and neither does a camera with no keywords. It returns nil
// when no camera has keywords, so a session without keywords records
// nothing.
func (t *tree) fitsKeywordsOf(ref serverRef, site string, devices []*device, reservation []observatory.FITSKeyword) map[string][]observatory.FITSKeyword {
	var observatoryLevel, telescopeLevel []observatory.FITSKeyword
	if o, ok := t.observatories[site]; ok {
		observatoryLevel = o.Spec.FITSKeywords
	}
	if telescope, ok := t.telescopes[ref.name]; ok {
		telescopeLevel = telescope.Spec.FITSKeywords
	}
	var out map[string][]observatory.FITSKeyword
	for _, d := range devices {
		if d.kind != observatory.CameraKind || t.guides(ref, d) {
			continue
		}
		merged := observatory.MergeFITSKeywords(observatoryLevel, telescopeLevel, d.object.Spec.FITSKeywords, reservation)
		if len(merged) == 0 {
			continue
		}
		if out == nil {
			out = map[string][]observatory.FITSKeyword{}
		}
		out[d.key()] = merged
	}
	return out
}

// clearFITSKeywords is the keyword name that libindi's CCD class reads
// as a request to remove every keyword that clients wrote
// (libs/indibase/indiccd.cpp). The driver ignores a write with an empty
// value, so that does not remove a keyword.
const clearFITSKeywords = "INDI_CLEAR"

// writeFITSKeywords clears an imaging camera's keywords and then writes
// each keyword that the session recorded for it, one write per keyword.
// It clears even when the record is empty, so a retry that removed a
// keyword from the record removes it from the driver. FITS_HEADER is
// write-only, so a step that runs again cannot compare and writes every
// keyword again. Each write sets all three members, because the
// driver keeps the value of a member that a write leaves out, and a
// keyword with no comment would get the comment of the one before it.
func writeFITSKeywords(ctx context.Context, t *tree, h handle, notes *[]string, note func(bool, error) error) error {
	if t.guides(h.d.recordedOn, h.d) {
		return nil
	}
	keywords := t.records[h.d.recordedOn].FITSKeywords[h.d.key()]
	if _, ok := h.property(ctx, "FITS_HEADER"); !ok {
		if len(keywords) > 0 {
			*notes = append(*notes, fmt.Sprintf("no FITS_HEADER on %s to write %d FITS keywords", h, len(keywords)))
		}
		return nil
	}
	clear := observatory.FITSKeyword{Name: clearFITSKeywords}
	for _, k := range append([]observatory.FITSKeyword{clear}, keywords...) {
		err := h.settle(ctx, func() (indi.Sent, error) {
			return h.client().SetTexts(h.name, "FITS_HEADER", map[string]string{
				"KEYWORD_NAME": k.Name, "KEYWORD_VALUE": k.Value, "KEYWORD_COMMENT": k.Comment,
			})
		})
		if err := note(true, err); err != nil {
			return fmt.Errorf("writing the FITS keyword %s: %w", k.Name, err)
		}
	}
	return nil
}
