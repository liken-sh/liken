package main

// appearancesfacelessrows.go is the walk's read of the faceless end that the
// appearances worker records in its ledger. The walk copies it into the
// video's file row, where the marks estimate reads it (markfaces.go).

// One video's answer as the walk reads it: the video's path, the size of the
// file the detections record came from, and the faceless end, nil where the
// answer was written before the worker read it.
type facelessAnswer struct {
	path     string
	size     int64
	faceless *timeSpan
}

func (s likenDir) facelessAnswers(entries []appearancesEntry) []facelessAnswer {
	answers := make([]facelessAnswer, 0, len(entries))
	for _, entry := range entries {
		answers = append(answers, facelessAnswer{
			path: s.itemOf(factAppearances, entry.Path), size: entry.Size, faceless: entry.Faceless})
	}
	return answers
}

// fillFaceless writes the start of each video's faceless end into its file
// row, where the answer came from a file of the row's size and the run
// holds at least one sample. It returns the videos whose answer holds no
// faceless end, whose found attempt the settle then reads as one with its
// output missing, so the worker reads the record again.
func (r *walkResult) fillFaceless() map[string]bool {
	answers := r.faceless
	r.faceless = nil
	unread := map[string]bool{}
	byPath := make(map[string]facelessAnswer, len(answers))
	for _, answer := range answers {
		if answer.faceless == nil {
			unread[answer.path] = true
			continue
		}
		byPath[answer.path] = answer
	}
	for at := range r.files {
		row := &r.files[at]
		answer, held := byPath[row.Path]
		if held && answer.size == row.SizeBytes && answer.faceless.End > answer.faceless.Start {
			row.FacelessTailMs = answer.faceless.Start
		}
	}
	return unread
}
