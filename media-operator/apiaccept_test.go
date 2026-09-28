package main

// These tests carry the negotiation rows the three capture API plans
// share: no Accept is the default, an exact type beats a discounted
// wildcard, a deprecated alias matches, and a form at q=0 is refused.
// Each row is one rule as the plan states it, so the table is the
// proof that the parser follows the plan.

import "testing"

func TestAcceptNegotiatesTheFormServed(t *testing.T) {
	cases := []struct {
		name   string
		accept string
		forms  []mediaForm
		want   string
		wantOK bool
	}{
		{
			name:   "no Accept on the composed forms",
			accept: "",
			forms:  composedForms,
			want:   "video/mp4",
			wantOK: true,
		},
		{
			name:   "an Accept of only spaces",
			accept: "   ",
			forms:  composedForms,
			want:   "video/mp4",
			wantOK: true,
		},
		{
			name:   "the registered matroska type",
			accept: "video/matroska",
			forms:  composedForms,
			want:   "video/matroska",
			wantOK: true,
		},
		{
			name:   "the deprecated matroska alias",
			accept: "video/x-matroska",
			forms:  composedForms,
			want:   "video/matroska",
			wantOK: true,
		},
		{
			name:   "an exact type over a discounted wildcard",
			accept: "video/*;q=0.5, video/matroska",
			forms:  composedForms,
			want:   "video/matroska",
			wantOK: true,
		},
		{
			name:   "an exact type over a wildcard at equal quality",
			accept: "video/*, video/matroska",
			forms:  composedForms,
			want:   "video/matroska",
			wantOK: true,
		},
		{
			name:   "a parameter with a quoted comma",
			accept: `video/mp4;codecs="avc1.64001f,Opus"`,
			forms:  composedForms,
			want:   "video/mp4",
			wantOK: true,
		},
		{
			name:   "an unreadable quality",
			accept: "video/matroska;q=high",
			forms:  composedForms,
			want:   "video/matroska",
			wantOK: true,
		},
		{
			name:   "an entry with no slash",
			accept: "matroska",
			forms:  composedForms,
			wantOK: false,
		},
		{
			name:   "no forms offered",
			accept: "",
			forms:  nil,
			wantOK: false,
		},
		{
			name:   "an image wildcard on the screen forms",
			accept: "image/*",
			forms:  screenForms,
			want:   "image/png",
			wantOK: true,
		},
		{
			name:   "a discounted default on the screen forms",
			accept: "video/mp4, image/png;q=0.5",
			forms:  screenForms,
			want:   "video/mp4",
			wantOK: true,
		},
		{
			name:   "the default excluded, the rest admitted",
			accept: "image/png;q=0, */*",
			forms:  screenForms,
			want:   "image/jpeg",
			wantOK: true,
		},
		{
			name:   "a type wildcard over the catch-all at equal quality",
			accept: "*/*, video/*",
			forms:  screenForms,
			want:   "video/mp4",
			wantOK: true,
		},
		{
			name:   "every offered type excluded",
			accept: "image/png;q=0",
			forms:  screenForms,
			wantOK: false,
		},
		{
			name:   "a type the screen forms do not serve",
			accept: "audio/wav",
			forms:  screenForms,
			wantOK: false,
		},
		{
			name:   "no Accept on the audio forms",
			accept: "",
			forms:  audioForms,
			want:   "audio/wav",
			wantOK: true,
		},
		{
			name:   "the catch-all on the audio forms",
			accept: "*/*",
			forms:  audioForms,
			want:   "audio/wav",
			wantOK: true,
		},
		{
			name:   "the audio wildcard",
			accept: "audio/*",
			forms:  audioForms,
			want:   "audio/wav",
			wantOK: true,
		},
		{
			name:   "a codecs parameter and a lower quality",
			accept: "audio/ogg; codecs=opus, audio/flac;q=0.5",
			forms:  audioForms,
			want:   "audio/ogg",
			wantOK: true,
		},
		{
			name:   "equal quality, the server's order",
			accept: "audio/flac;q=0.5, audio/ogg;q=0.5",
			forms:  audioForms,
			want:   "audio/flac",
			wantOK: true,
		},
		{
			name:   "an uppercase quality parameter",
			accept: "audio/flac;Q=0.9, audio/ogg;q=0.8",
			forms:  audioForms,
			want:   "audio/flac",
			wantOK: true,
		},
		{
			name:   "the default excluded on the audio forms",
			accept: "audio/wav;q=0, */*",
			forms:  audioForms,
			want:   "audio/flac",
			wantOK: true,
		},
		{
			name:   "a type the audio forms do not serve",
			accept: "audio/mpeg",
			forms:  audioForms,
			wantOK: false,
		},
		{
			name:   "the registered RIFF WAVE name",
			accept: "audio/vnd.wave",
			forms:  audioForms,
			want:   "audio/wav",
			wantOK: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form, ok := negotiate(c.accept, c.forms)
			mustMatch(t, ok, c.wantOK)
			mustMatch(t, form.Type, c.want)
		})
	}
}

func TestAcceptableAdmitsOneFixedForm(t *testing.T) {
	cases := []struct {
		name   string
		accept string
		form   mediaForm
		want   bool
	}{
		{
			name:   "no Accept",
			accept: "",
			form:   composedForms[0],
			want:   true,
		},
		{
			name:   "the catch-all",
			accept: "*/*",
			form:   composedForms[0],
			want:   true,
		},
		{
			name:   "a type wildcard",
			accept: "video/*",
			form:   composedForms[1],
			want:   true,
		},
		{
			name:   "the form's own type",
			accept: "video/mp4",
			form:   composedForms[0],
			want:   true,
		},
		{
			name:   "the form's alias",
			accept: "video/x-matroska",
			form:   composedForms[1],
			want:   true,
		},
		{
			name:   "another form's type",
			accept: "video/mp4",
			form:   composedForms[1],
			want:   false,
		},
		{
			name:   "an image type against the composed form",
			accept: "image/png",
			form:   composedForms[0],
			want:   false,
		},
		{
			name:   "the form at zero quality",
			accept: "video/matroska;q=0",
			form:   composedForms[1],
			want:   false,
		},
		{
			name:   "a RIFF WAVE alias",
			accept: "audio/vnd.wave",
			form:   audioForms[0],
			want:   true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustMatch(t, acceptable(c.accept, c.form), c.want)
		})
	}
}

func TestAcceptFindsAFormByExtension(t *testing.T) {
	cases := []struct {
		name      string
		extension string
		forms     []mediaForm
		want      string
		wantOK    bool
	}{
		{
			name:      "the composed default",
			extension: "mp4",
			forms:     composedForms,
			want:      "video/mp4",
			wantOK:    true,
		},
		{
			name:      "the matroska extension",
			extension: "mkv",
			forms:     composedForms,
			want:      "video/matroska",
			wantOK:    true,
		},
		{
			name:      "a screen extension",
			extension: "png",
			forms:     screenForms,
			want:      "image/png",
			wantOK:    true,
		},
		{
			name:      "the Ogg Opus extension",
			extension: "opus",
			forms:     audioForms,
			want:      "audio/ogg",
			wantOK:    true,
		},
		{
			name:      "an extension the composed forms do not serve",
			extension: "webm",
			forms:     composedForms,
			wantOK:    false,
		},
		{
			name:      "no extension",
			extension: "",
			forms:     composedForms,
			wantOK:    false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form, ok := formByExtension(c.extension, c.forms)
			mustMatch(t, ok, c.wantOK)
			mustMatch(t, form.Type, c.want)
		})
	}
}
