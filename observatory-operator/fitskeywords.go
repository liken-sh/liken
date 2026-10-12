package main

// The declared FITS keywords of plan 20. The Observatory, the
// Telescope, the Camera, and the Reservation can each declare keywords
// in spec.fitsKeywords, and Configure writes the merged list to each
// imaging camera's FITS_HEADER. The driver then writes the keywords into
// every frame when it creates the file, frames that a desktop client
// takes included, so no component edits a frame afterward.
//
// The driver puts each keyword it receives on every later frame until
// its process exits, or until a client clears them all. So the PowerOn
// step records the merged list of each imaging camera in
// status.session, and Configure clears the camera's keywords and writes
// the record, never the specs. An edit takes effect at the next
// session, and every frame of one session carries the same declared
// keywords. A driver that comes back after
// its pod restarts is a new process with no keywords, and the runner
// configures it again (steady.go), which writes the record again.

import (
	"context"
	"fmt"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// fitsKeywordsOf answers the merged keywords of each imaging camera
// among a telescope's devices, by the device's key, from the
// Observatory site, the Telescope, the camera, and the reservation's
// own keywords. A camera that a Guider guides with sends its frames to
// PHD2 and writes no file, so it has none, and neither has a camera
// whose merged list is empty. It answers nil when no camera has
// keywords, so a session with none records nothing.
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

// clearFITSKeywords is the name that libindi's CCD reads, in a write
// to FITS_HEADER, as a request to remove every keyword that a client
// wrote before (libs/indibase/indiccd.cpp). An empty value does not
// remove a keyword: the driver ignores such a write.
const clearFITSKeywords = "INDI_CLEAR"

// writeFITSKeywords clears the keywords of an imaging camera and writes
// each keyword that the session recorded for it, one write for each
// keyword. The clear comes first even when the record holds none, so a
// retry that removes a keyword from the record removes it from the
// driver too. FITS_HEADER is write-only, so the driver reports no
// keywords to compare with, and a step that runs again writes them all
// again. Each write names all three members: the driver keeps the
// members that a write leaves out, so a keyword with no comment would
// take the comment of the keyword before it.
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
