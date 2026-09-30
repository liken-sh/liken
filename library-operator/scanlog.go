package main

// scanlog.go holds the lines a walk and a rescan write about the facts they
// reopened. A gap count that grows says there is work, and these lines say
// why: a file replaced at its path, or an output that is gone.

// One line for each replaced file, with the size the probe record holds and
// the size on the volume, as the collector takes the folder. A replaced file
// is an event at human scale, one import at a time, so each one gets a line.
func (s *scanner) logReplaced(reopened reopenedFacts) {
	for _, file := range reopened.replaced {
		s.logf("replaced file %s: %d bytes in the probe record, %d on the volume",
			s.named(file.path), file.recorded, file.found)
	}
}

// One line of counts for the read, and none where it reopened nothing. The
// missing outputs get a count and no line each, because one deleted tree of
// tiles or art can be thousands of them.
func (s *scanner) logReopened(reopened reopenedFacts) {
	if len(reopened.replaced) == 0 && reopened.missing == 0 {
		return
	}
	s.logf("reopened the facts of %d replaced files and %d missing outputs",
		len(reopened.replaced), reopened.missing)
}
