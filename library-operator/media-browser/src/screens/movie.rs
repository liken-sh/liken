// A movie's page. The backdrop draws full bleed under the text, and the
// page reads down: the logo or the title, the facts, the tagline, the
// plot, the buttons, the set strip, a strip for each franchise the movie
// belongs to, and the stripes of credited people.
// Focus lands on Play, so a film is two presses from the wall, as it was
// when the wall played it on select.

mod page;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Rectangle, Theme};

use super::franchise::strips::{self, Move, Place, Strips};
use super::{Item, Screen, Step, facts, foot, franchise, person, stripes};
use crate::catalog::draw::Date;
use crate::catalog::{MovieDetails, MovieSet, Query, Selection, Slot, Source};
use crate::focus;
use crate::posters::Posters;
use crate::views::curtain::{Curtain, Head, Layer};
use crate::views::{layers, ratings};

/// Where focus is on the page.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Focus {
    /// One button of the row.
    Buttons(usize),
    /// One member of the set strip.
    Strip(usize),
    /// One rung of the franchise strips: which strip, and the heading or
    /// the member in it.
    Franchise(usize, Place),
    /// One headshot of one stripe: the stripe, and the slot in it.
    Stripe(usize, usize),
}

/// The words after a set's name on its strip heading. The count is the
/// members of the set, which is every film it holds. A set is narrower
/// than a franchise, and the heading says which of the two a strip is. A
/// set of one never reaches a page, because a set needs two members.
fn films(count: usize) -> String {
    format!("a {count}-film set")
}

/// The set the movie belongs to, as the strip draws it.
#[derive(Debug)]
pub struct Set {
    /// The strip's heading: the set's own title and the count of its
    /// films.
    pub heading: String,
    /// Every movie in the set, in release order.
    pub members: Vec<Item>,
    /// The index of the member this page is about.
    pub current: usize,
}

impl Set {
    // The members are the slots of a `Set` query, so the strip draws the
    // same slots a wall of the set would. `id` names the member this page is
    // about.
    fn of(set: MovieSet, query: &Query, id: &str) -> Option<Self> {
        let Query::Set { library, .. } = query else {
            return None;
        };
        let mut members: Vec<Item> = set
            .members
            .into_iter()
            .map(|member| Item::of(query, Slot::of(library, "movies", member)))
            .collect();
        // The strip draws the card's two lines, so both are cut by the
        // shaper here at the read, and not on every frame.
        super::fitted_strip(&mut members);
        let current = members.iter().position(|member| member.id == id)?;
        Some(Self {
            heading: facts::joined(&[&set.title, &films(members.len())]),
            members,
            current,
        })
    }
}

/// The movie page: the words it draws, the art it draws them over, and
/// where focus is. Every line is built once here, at the read, and not
/// on every frame.
#[derive(Debug)]
pub struct Movie {
    /// The catalog's library column, `namespace/name`.
    pub library: String,
    /// The movie's id inside that library.
    pub id: String,
    /// The name a person reads. The page draws it where the movie has no
    /// logo.
    pub title: String,
    /// The path of the logo file, empty where the movie has none.
    pub logo: String,
    /// The path of the backdrop file, empty where the movie has none.
    pub backdrop: String,
    /// Whether the movie holds a trailer file. That is what puts the
    /// second button on the row.
    pub trailer: bool,
    /// The year, the runtime, the content rating, and the genres, on one
    /// line.
    pub facts: String,
    /// The scores the ratings line draws, in the order it draws them.
    pub ratings: Vec<ratings::Score>,
    /// The tagline, empty where the sidecar named none.
    pub tagline: String,
    /// The plot. The page cuts it to four lines.
    pub plot: String,
    /// The credited people, as the stripes at the end of the page.
    pub stripes: stripes::Stripes,
    /// The studios and the files, as the block after the last stripe.
    pub foot: foot::Foot,
    /// The set the movie belongs to, or nothing where it belongs to none.
    pub set: Option<Set>,
    /// The franchises the movie belongs to, one strip each, under the set
    /// strip.
    pub franchises: Strips,
    /// Where focus is.
    pub focus: Focus,
}

impl Movie {
    /// Read one movie's page, or nothing where the library holds no
    /// movie under that id. Focus lands on Play.
    pub fn open(library: &str, id: &str, source: &mut dyn Source) -> Option<Self> {
        let details = source.movie(library, id)?;
        let set = set_of(library, id, &details, source);
        Some(Self {
            library: library.to_string(),
            id: id.to_string(),
            title: details.title.clone(),
            logo: details.logo.clone(),
            backdrop: details.backdrop.clone(),
            trailer: !details.trailer.is_empty(),
            facts: facts_of(&details),
            ratings: ratings::scores(&details.ratings),
            tagline: details.tagline.clone(),
            plot: details.plot.clone(),
            stripes: stripes::Stripes::of(source.credits(library, id)),
            foot: foot::Foot::of(&details.studios, &source.files(library, id)),
            set,
            franchises: Strips::of(library, id, source),
            focus: Focus::Buttons(0),
        })
    }

    /// Read the page again, because the scanner can write the movie or
    /// its set while the page is open. Focus stays where it was.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let Some(fresh) = Self::open(&self.library, &self.id, source) else {
            return;
        };
        let focus = self.focus;
        *self = fresh;
        self.focus = self.hold(focus);
    }

    /// The buttons this page draws. Play is always there. Trailer joins
    /// it where the `files` table holds a trailer for the movie.
    pub fn buttons(&self) -> &'static [&'static str] {
        if self.trailer {
            &["Play", "Trailer"]
        } else {
            &["Play"]
        }
    }

    /// Fold one press in. Left and right move across the row that holds
    /// focus, down reaches the set strip, then the franchise strips, and
    /// then the stripes, and up climbs back to the buttons. A franchise
    /// strip's heading is a rung over its members.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        match self.focus {
            Focus::Buttons(index) => self.on_button(index, key),
            Focus::Strip(index) => self.on_strip(index, key, source),
            Focus::Franchise(strip, place) => self.on_franchise((strip, place), key, source),
            Focus::Stripe(stripe, slot) => self.on_stripe((stripe, slot), key, source),
        }
    }

    /// The view: the backdrop, the scrim over it, the page over both, and
    /// the loading state's curtain over the page while that state runs.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
        curtain: Option<Curtain>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        layers::Page {
            library: &self.library,
            art: &self.backdrop,
            posters,
            ground: layers::Ground::None,
            front: page::Page {
                movie: self,
                posters,
                lifted: curtain.is_some(),
            },
            over: curtain.map(|curtain| Layer {
                library: &self.library,
                art: &self.backdrop,
                logo: &self.logo,
                name: &self.title,
                posters,
                head: self,
                curtain,
            }),
        }
        .view()
    }

    fn on_button(&mut self, index: usize, key: &str) -> Step {
        match key {
            "enter" => Step::Play {
                library: self.library.clone(),
                selection: self.chosen(index),
            },
            "down" => {
                self.focus = self.below(index);
                Step::Stay
            }
            _ => {
                self.focus = Focus::Buttons(focus::row(index, self.buttons().len(), key));
                Step::Stay
            }
        }
    }

    fn on_strip(&mut self, index: usize, key: &str, source: &mut dyn Source) -> Step {
        let Some(set) = &self.set else {
            self.focus = Focus::Buttons(0);
            return Step::Stay;
        };
        match key {
            "enter" => {
                let Some(member) = set.members.get(index) else {
                    return Step::Stay;
                };
                if member.id == self.id {
                    return Step::Stay;
                }
                // A sibling replaces this page and does not cover it, so
                // back from any film of a set returns to the wall. The
                // strip is a way to move inside a set, not a screen of
                // its own.
                match Self::open(&self.library, &member.id, source) {
                    Some(page) => Step::Replace(Screen::Movie(Box::new(page))),
                    None => Step::Stay,
                }
            }
            "up" => {
                self.focus = Focus::Buttons(0);
                Step::Stay
            }
            "down" => {
                self.focus = self.under_strip();
                Step::Stay
            }
            _ => {
                self.focus = Focus::Strip(focus::row(index, set.members.len(), key));
                Step::Stay
            }
        }
    }

    // One press on a stripe. Select opens the person's page, and a
    // name the credits could not resolve opens nothing.
    fn on_stripe(&mut self, rung: stripes::Rung, key: &str, source: &mut dyn Source) -> Step {
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
            None => self.above(),
        };
        Step::Stay
    }

    // The rung under the buttons: the set strip where the movie is in a
    // set, then the franchise strips, then the first stripe, and the
    // buttons themselves where the page holds none of the three.
    fn below(&self, index: usize) -> Focus {
        if let Some(set) = &self.set {
            return Focus::Strip(set.current);
        }
        match self.franchises.first() {
            Some((strip, place)) => Focus::Franchise(strip, place),
            None => match self.stripes.first() {
                Some((stripe, slot)) => Focus::Stripe(stripe, slot),
                None => Focus::Buttons(index),
            },
        }
    }

    // The rung under the set strip: the first franchise strip, then the
    // first stripe, and the set strip itself where the page holds
    // neither.
    fn under_strip(&self) -> Focus {
        match self.franchises.first() {
            Some((strip, place)) => Focus::Franchise(strip, place),
            None => match self.stripes.first() {
                Some((stripe, slot)) => Focus::Stripe(stripe, slot),
                None => self.focus,
            },
        }
    }

    // The rung over the first stripe: the last franchise strip, the set
    // strip where the movie is in a set, and the buttons where the page
    // holds neither.
    fn above(&self) -> Focus {
        if let Some((strip, place)) = self.franchises.last() {
            return Focus::Franchise(strip, place);
        }
        match &self.set {
            Some(set) => Focus::Strip(set.current),
            None => Focus::Buttons(0),
        }
    }

    // The rung over the franchise strips: the set strip where the movie
    // is in a set, and the buttons where it is not.
    fn over_franchises(&self) -> Focus {
        match &self.set {
            Some(set) => Focus::Strip(set.current),
            None => Focus::Buttons(0),
        }
    }

    // One press on a franchise strip. A select on the heading opens the
    // franchise's page, and a select on a member replaces this page
    // with that member's, the way a sibling in a set strip does.
    fn on_franchise(&mut self, rung: strips::Rung, key: &str, source: &mut dyn Source) -> Step {
        if key == "enter" {
            return franchise_press(&self.franchises, rung, source);
        }
        self.focus = match self.franchises.key(rung, key) {
            Move::To((strip, place)) => Focus::Franchise(strip, place),
            Move::Above => self.over_franchises(),
            Move::Below => match self.stripes.first() {
                Some((stripe, slot)) => Focus::Stripe(stripe, slot),
                None => Focus::Franchise(rung.0, rung.1),
            },
        };
        Step::Stay
    }

    // The choice a button stands for. Only a movie with a trailer file
    // has a second button, so index one is always the trailer.
    fn chosen(&self, index: usize) -> Selection {
        match index {
            0 => Selection::Movie {
                id: self.id.clone(),
            },
            _ => Selection::Trailer {
                id: self.id.clone(),
            },
        }
    }

    // Where focus lands after a re-read: where it was, unless the row it
    // was on grew shorter or the set went away.
    fn hold(&self, focus: Focus) -> Focus {
        match focus {
            Focus::Buttons(index) => Focus::Buttons(index.min(self.buttons().len() - 1)),
            Focus::Strip(index) => match &self.set {
                Some(set) => Focus::Strip(index.min(set.members.len() - 1)),
                None => Focus::Buttons(0),
            },
            Focus::Franchise(strip, place) => match self.franchises.held((strip, place)) {
                Some((strip, place)) => Focus::Franchise(strip, place),
                None => Focus::Buttons(0),
            },
            Focus::Stripe(stripe, slot) => match self.stripes.held((stripe, slot)) {
                Some((stripe, slot)) => Focus::Stripe(stripe, slot),
                None => Focus::Buttons(0),
            },
        }
    }
}

impl Head for Movie {
    fn head(&self, bounds: Rectangle) -> Rectangle {
        page::head(self, bounds)
    }
}

/// What a select on a franchise strip opens. The heading opens the
/// franchise's own page, which covers this one, so back returns here. A
/// member replaces this page, because it is another title of the same
/// story and not a screen of its own. The movie page and the series page
/// share this one rule.
pub(crate) fn franchise_press(
    franchises: &Strips,
    rung: strips::Rung,
    source: &mut dyn Source,
) -> Step {
    if let Place::Heading = rung.1 {
        let Some(band) = franchises.band(rung) else {
            return Step::Stay;
        };
        return match franchise::Franchise::open(&band.library, &band.id, source) {
            Some(page) => Step::Open(Screen::Franchise(Box::new(page))),
            None => Step::Stay,
        };
    }
    let Some(member) = franchises.member(rung) else {
        return Step::Stay;
    };
    let opened = match member.kind.as_str() {
        "movies" => Movie::open(&member.library, &member.id, source)
            .map(|page| Screen::Movie(Box::new(page))),
        _ => super::series::Series::open(&member.library, &member.id, source)
            .map(|page| Screen::Series(Box::new(page))),
    };
    match opened {
        Some(screen) => Step::Replace(screen),
        None => Step::Stay,
    }
}

fn set_of(library: &str, id: &str, details: &MovieDetails, source: &mut dyn Source) -> Option<Set> {
    if details.set_id.is_empty() {
        return None;
    }
    let query = Query::Set {
        library: library.to_string(),
        id: details.set_id.clone(),
    };
    Set::of(source.set(library, &details.set_id)?, &query, id)
}

/// The facts line of one movie's page: the date, the runtime, the
/// content rating, and the genres, on one line.
pub(crate) fn facts_of(details: &MovieDetails) -> String {
    facts::joined(&[&facts_without_genres(details), &details.genres.join(", ")])
}

/// The date, the runtime, and the content rating. The banner reads this
/// line, because it draws the genres on a line of their own.
/// The date is spelled against today, which the line reads at the read
/// and not on every frame.
pub(crate) fn facts_without_genres(details: &MovieDetails) -> String {
    facts::joined(&[
        &facts::date_worded(&details.released, &Date::today().iso()),
        &facts::runtime(details.duration),
        &details.rating,
    ])
}

#[cfg(test)]
mod tests;
