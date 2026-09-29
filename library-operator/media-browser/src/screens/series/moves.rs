// The moves across a series' page outside an episode's row: the series'
// own button row, the wall of stills, the seasons rail beside it, and the
// franchise strips and the stripes of people under it. `episode.rs` holds
// the moves inside an episode's row.

use super::{COLUMNS, Focus, Series, seasons};
use crate::catalog::{Selection, Source};
use crate::focus::{self, Run};
use crate::screens::franchise::strips::{self, Move};
use crate::screens::movie::{franchise_press, row};
use crate::screens::{Screen, Step, person, stripes};

impl Series {
    // One press while the button row holds focus. The row is beside the
    // header's text column, over the first row of stills. Up moves no
    // focus, so the press reaches the browser's strip. Down and left
    // return to the still the wall last held, or to the block under the
    // wall where the series has no episodes.
    pub(super) fn on_button(&mut self, index: usize, key: &str, source: &mut dyn Source) -> Step {
        match key {
            "enter" => match self.buttons().get(index) {
                // A trailer is in no order, so it offers nothing after it.
                Some(row::Button::Trailer) => Step::Play {
                    library: self.library.clone(),
                    selection: Selection::Trailer {
                        id: self.id.clone(),
                    },
                    start: None,
                    next: None,
                },
                _ => Step::Stay,
            },
            "up" => Step::Still,
            "down" | "left" => {
                self.focus = match self.stills.is_empty() {
                    true => self.under_wall(0),
                    false => Focus::Still(self.entered.min(self.stills.len() - 1)),
                };
                self.refoot(source);
                Step::Stay
            }
            _ => {
                self.focus = Focus::Buttons(focus::row(index, self.buttons().len(), key));
                Step::Stay
            }
        }
    }

    pub(super) fn on_still(&mut self, index: usize, key: &str, source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.entered = index;
            self.focus = match key {
                "right" => match seasons::onto(self, index) {
                    Some(bar) => Focus::Rail(bar),
                    None => self.moved(index, key),
                },
                _ => self.moved(index, key),
            };
            self.refoot(source);
            return Step::Stay;
        }
        // Select opens the episode's row, which holds the buttons a movie's
        // page holds, so an episode resumes, starts over, and takes a mark
        // the way a film does.
        if index < self.stills.len() {
            self.focus = Focus::Episode(index, 0);
        }
        Step::Stay
    }

    // Where one press inside the wall lands: a still, or the rung under
    // the wall where down leaves the last row.
    fn moved(&self, index: usize, key: &str) -> Focus {
        let runs: Vec<Run> = self.seasons.iter().map(|season| season.run).collect();
        let moved = focus::sectioned(index, &runs, COLUMNS, key);
        match (key, moved == index) {
            ("down", true) => self.under_wall(moved),
            ("up", true) => self.over_wall(moved),
            _ => Focus::Still(moved),
        }
    }

    // The rung over the first row of stills: the button row where the
    // series holds a trailer, and the still itself where it holds none.
    fn over_wall(&self, index: usize) -> Focus {
        match self.trailer {
            true => Focus::Buttons(0),
            false => Focus::Still(index),
        }
    }

    // One press while a bar of the rail holds focus. The foot is read
    // again because a left or a select puts focus back on a still.
    pub(super) fn on_rail(&mut self, bar: usize, key: &str, source: &mut dyn Source) -> Step {
        self.focus = seasons::key(self, bar, key);
        self.refoot(source);
        Step::Stay
    }

    // The rung under the last row of stills: the first franchise strip,
    // then the first stripe, and the still itself where the page holds
    // neither.
    fn under_wall(&self, index: usize) -> Focus {
        if let Some((strip, place)) = self.franchises.first() {
            return Focus::Franchise(strip, place);
        }
        match self.stripes.first() {
            Some((stripe, slot)) => Focus::Stripe(stripe, slot),
            None => Focus::Still(index),
        }
    }

    // The rung over the first stripe: the last franchise strip, and the
    // wall's last still where the page holds none.
    fn over_stripes(&self, rung: stripes::Rung) -> Focus {
        if let Some((strip, place)) = self.franchises.last() {
            return Focus::Franchise(strip, place);
        }
        match self.stills.len() {
            0 => Focus::Stripe(rung.0, rung.1),
            count => Focus::Still(count - 1),
        }
    }

    // One press on a franchise strip. A select on the heading opens the
    // franchise's page, and a select on a member opens that member's, the
    // way it does from a film's page.
    pub(super) fn on_franchise(
        &mut self,
        rung: strips::Rung,
        key: &str,
        source: &mut dyn Source,
    ) -> Step {
        if key == "enter" {
            return franchise_press(&self.franchises, rung, source);
        }
        self.focus = match self.franchises.key(rung, key) {
            Move::To((strip, place)) => Focus::Franchise(strip, place),
            Move::Above => match self.stills.len() {
                0 => Focus::Franchise(rung.0, rung.1),
                count => Focus::Still(count - 1),
            },
            Move::Below => match self.stripes.first() {
                Some((stripe, slot)) => Focus::Stripe(stripe, slot),
                None => Focus::Franchise(rung.0, rung.1),
            },
        };
        self.refoot(source);
        Step::Stay
    }

    // One press on a stripe. Select opens the person's page, and a
    // name the credits could not resolve opens nothing.
    pub(super) fn on_stripe(
        &mut self,
        rung: stripes::Rung,
        key: &str,
        source: &mut dyn Source,
    ) -> Step {
        if key == "enter" {
            let Some(face) = self.stripes.face(rung) else {
                return Step::Stay;
            };
            if face.contributor.is_empty() {
                return Step::Stay;
            }
            return match person::Person::open(&self.library, &face.contributor, source) {
                Some(page) => Step::Open(Screen::Person(Box::new(page))),
                None => Step::Stay,
            };
        }
        self.focus = match self.stripes.key(rung, key) {
            Some((stripe, slot)) => Focus::Stripe(stripe, slot),
            // Up from the first stripe returns to the last franchise
            // strip, then to the wall's last row, and stays where the
            // series holds neither.
            None => self.over_stripes(rung),
        };
        self.refoot(source);
        Step::Stay
    }
}
