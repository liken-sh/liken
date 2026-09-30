// The `Person` list as it changes under a running browser. The watch in
// `audience::watch` reads the file on every change, and each pass of the
// loop takes what it read here. The audience holds the one list, and every
// place that draws people reads from it: the picker's tiles, names, and
// faces, the strip's circles, the continue-watching row's heading, and the
// room's stack on a franchise page. Three of those keep something cut from
// the list, so a new list refreshes each of them: the picker's state, the
// faces, and the letters the pages were read with.
//
// A new list changes no answer to who is watching. A person the list
// dropped stays in the answer until the next answer is taken, which is
// the rule `Audience::learn` states.

use super::{Browser, lines};
use crate::art::Art;
use crate::audience::Person;
use crate::catalog::Source;

impl<S: Source, A: Art> Browser<S, A> {
    // Take what the watch read since the last pass. A read that failed
    // prints its line and changes nothing, so the browser keeps the last
    // list that parsed. The answer is whether the list changed, which is
    // a frame to draw.
    pub(super) fn follow_people(&mut self) -> bool {
        let Some(watch) = &self.people else {
            return false;
        };
        let delivery = watch.take();
        for failure in delivery.failures {
            self.log.line(format!(
                "the Person list could not be read, and the browser keeps the list it holds: {failure}"
            ));
        }
        delivery.people.is_some_and(|people| self.relist(people))
    }

    // Take a new `Person` list. A list equal to the one held changes
    // nothing. The answer is whether the list changed.
    pub(super) fn relist(&mut self, people: Vec<Person>) -> bool {
        if people == self.audience.known() {
            return false;
        }
        let letters = self.audience.letters(self.clock);
        let watching = self.audience.watching(self.clock);
        let count = people.len();
        let before = self.audience.learn(people);
        self.log.line(format!(
            "took a new Person list of {}",
            lines::count(count, "name")
        ));
        self.repick(&before);
        // The pages hold the letters they were read with, so a new display
        // name reads the progress of the screen on top again and marks
        // the continue-watching row behind.
        if self.audience.letters(self.clock) != letters {
            self.progress_again();
        }
        // The message on the bus carries each display name, so a renamed
        // person in the room goes out again under the stamp it had.
        if self.audience.watching(self.clock) != watching {
            self.republish_audience();
        }
        true
    }

    // Map a picker that stands across to the new list, and cut the faces
    // of the people whose picture changed. The faces keep every picture
    // they already cut, so only a new or changed picture is decoded. A
    // list with nobody in it closes the picker, because a browser with
    // an empty `Person` list asks nobody.
    fn repick(&mut self, before: &[Person]) {
        let Some(picker) = &self.picker else {
            return;
        };
        let known = self.audience.known();
        if known.is_empty() {
            self.picker = None;
            return;
        }
        self.picker = Some(picker.relisted(before, known));
        self.faces.refresh(known, self.scale);
    }
}
