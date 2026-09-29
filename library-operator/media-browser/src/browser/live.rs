// The progress store's changes, read for the works a screen draws. Every
// writer of the store writes a row, and the screen pod's own progress agent
// names each play whose rows changed on its update stream once the row is
// in the file the browser reads. So a mark pressed on this screen, a mark
// pressed on another, a `Play`'s position, and an outside play from
// Jellyfin all reach every browser the same way, and a read that follows
// the event reads the row.
//
// A change is matched against the screen on top, and only that screen's
// progress is read again. A page reads its one work, a wall reads the bars
// of its films, and a franchise page reads its members, because where the
// room stands in a franchise depends on every member. The home page reads
// its continue-watching row alone, and only for a play that names everyone
// in the room, because the row reads no other play. Every other row of the
// home page holds no progress. A change to a work the screen does not draw
// costs the resolve of the play and nothing more.

use super::Browser;
use crate::art::Art;
use crate::catalog::{Change, Source, Touched};

impl<S: Source, A: Art> Browser<S, A> {
    // Read the progress of the screen on top again, where a play the stream
    // named reaches it. A play that names everyone in the room marks the
    // continue-watching row behind, and the row is read once the home page
    // is on top. A hidden screen reads nothing and holds one read of its
    // progress for the first pass it shows again.
    //
    // The answer is whether the screen on top changed, which is a frame to
    // draw.
    pub(super) fn touch(&mut self, touched: &[Touched]) -> bool {
        if touched.is_empty() {
            return false;
        }
        if !self.refresh.shown() {
            self.refresh.changed(Change::Progress);
            return false;
        }
        let people = self.audience.current(self.clock).to_vec();
        if touched.iter().any(|play| play.covers(&people)) {
            self.row_stale = true;
        }
        let Some(top) = self.stack.last_mut() else {
            return false;
        };
        let drawn = touched.iter().any(|play| {
            play.reaches(&people)
                && play
                    .works
                    .iter()
                    .any(|(library, item)| top.draws_progress_of(library, item))
        });
        if !drawn {
            return false;
        }
        let letters = self.audience.letters(self.clock);
        top.read_progress(&mut self.source, &people, &letters);
        true
    }

    // Read every progress the screen on top draws, for a change no play
    // names: a stream that dropped, or plays that changed while the screen
    // was hidden. The continue-watching row is marked behind with it.
    pub(super) fn progress_again(&mut self) -> bool {
        self.row_stale = true;
        let people = self.audience.current(self.clock).to_vec();
        let letters = self.audience.letters(self.clock);
        let Some(top) = self.stack.last_mut() else {
            return false;
        };
        top.read_progress(&mut self.source, &people, &letters);
        true
    }

    // Ask the reader for the continue-watching row alone, where it is behind
    // and the home page is on top. A page over the home page leaves the row
    // behind, and back reads it, so a play upstairs costs a screen that
    // shows a page nothing.
    //
    // The answer is whether the row landed, which is a frame to draw.
    pub(super) fn refresh_row(&mut self) -> bool {
        if !self.row_stale || !self.stack.is_empty() {
            return false;
        }
        self.row_stale = false;
        let people = self.audience.current(self.clock).to_vec();
        let letters = self.audience.letters(self.clock);
        self.reader.ask_row(&mut self.source, people, letters);
        self.landed_home()
    }
}
