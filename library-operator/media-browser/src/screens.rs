// A kind is a plugin in the scanner and a screen design in the browser.
// This module holds the entries of the navigation stack: the home page
// at the bottom, and the screens each kind descends into. A screen holds
// the rows it read, decides what a press does, and composes the
// primitives in the views module. A new kind adds a screen here and,
// where it needs one, a primitive there. It adds no row to a table.

pub mod facts;
pub mod foot;
pub mod franchise;
pub mod home;
pub mod loading;
pub mod movie;
pub mod person;
pub mod series;
pub mod slots;
pub mod stripes;
pub mod volume;
pub mod wall;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Theme};

use crate::catalog::{InSeries, Query, Selection, Slot, Source};
use crate::posters::Posters;
use crate::views::curtain::Curtain;
use crate::views::{
    Card,
    wall::{POSTER, STILL},
};

/// One entry of the navigation stack.
pub enum Screen {
    /// The home page, the screen the browser opens on and the bottom of
    /// the stack.
    Home(home::Home),
    /// The slots one query answers, as a wall of art under a band that
    /// carries the query's heading.
    Wall(wall::Wall),
    /// One movie's page. It is boxed because it is much the largest
    /// variant, and every stack entry would otherwise carry its size.
    Movie(Box<movie::Movie>),
    /// One series' page, boxed for the reason a movie's page is.
    Series(Box<series::Series>),
    /// One person's page, boxed for the reason a movie's page is.
    Person(Box<person::Person>),
    /// One franchise's page, boxed for the reason a movie's page is.
    Franchise(Box<franchise::Franchise>),
}

/// What a press asks the browser to do. A screen reads the catalog and
/// moves its own focus. Only the browser holds the stack and the bus, so
/// a screen names the screen it opens and never pushes one itself.
pub enum Step {
    /// The press changed the screen alone, or changed nothing.
    Stay,
    /// Push this screen over the one that answered.
    Open(Screen),
    /// Put this screen in the place of the one that answered, so back
    /// climbs to the screen that one was opened from.
    Replace(Screen),
    /// Ask the operator to play what the person chose.
    Play {
        /// The library the choice resolves against.
        library: String,
        /// What the person chose.
        selection: Selection,
    },
}

impl Screen {
    /// Fold one press into the screen and answer what it asks the
    /// browser for.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        match self {
            Self::Home(screen) => screen.key(key, source),
            Self::Wall(screen) => screen.key(key, source),
            Self::Movie(screen) => screen.key(key, source),
            Self::Series(screen) => screen.key(key, source),
            Self::Person(screen) => screen.key(key, source),
            Self::Franchise(screen) => screen.key(key, source),
        }
    }

    /// Read this screen's rows again. A change that landed while the
    /// screen was covered was folded into the screen shown at the time,
    /// so the uncovered screen reads for itself.
    pub fn reread(&mut self, source: &mut dyn Source) {
        match self {
            Self::Home(screen) => screen.reread(source),
            Self::Wall(screen) => screen.reread(source),
            Self::Movie(screen) => screen.reread(source),
            Self::Series(screen) => screen.reread(source),
            Self::Person(screen) => screen.reread(source),
            Self::Franchise(screen) => screen.reread(source),
        }
    }

    /// Whether a rest of focus on this screen is worth a prefetch. Only a
    /// wall whose select opens a page over art answers true, so a press
    /// on any other screen schedules no frame.
    /// Whether the screen asks for a backdrop while focus rests. The home
    /// page answers as a wall does while a strip holds focus.
    pub fn prefetches(&self) -> bool {
        match self {
            Self::Home(screen) => screen.prefetches(),
            Self::Wall(screen) => screen.prefetches(),
            Self::Person(_) => true,
            _ => false,
        }
    }

    /// The library and the backdrop path of the page under the focused
    /// item, so the store decodes it while focus rests and the page
    /// opens with it drawn. A screen that opens no page over art answers
    /// nothing.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        match self {
            Self::Home(screen) => screen.resting(source),
            Self::Wall(screen) => screen.resting(source),
            Self::Person(screen) => screen.resting(source),
            _ => None,
        }
    }

    /// Read the files this screen draws that live on a library
    /// volume and not in the catalog. Only a person's page holds one, and
    /// every other screen reads nothing.
    pub fn volume<P: Posters>(&mut self, posters: &P) {
        if let Self::Person(screen) = self {
            screen.read_biography(posters);
        }
    }

    /// The view of this screen, with its art drawn from the store. Only
    /// the two screens a title plays from draw the loading state, and
    /// every other screen ignores it.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
        curtain: Option<Curtain>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        match self {
            Self::Home(screen) => screen.view(posters),
            Self::Wall(screen) => screen.view(posters),
            Self::Movie(screen) => screen.view(posters, curtain),
            Self::Series(screen) => screen.view(posters, curtain),
            Self::Person(screen) => screen.view(posters),
            Self::Franchise(screen) => screen.view(posters),
        }
    }
}

/// One title, as a wall slot or a strip poster draws it. Every item
/// carries its own library and kind, because a select opens the page for
/// the slot's kind and a wall may span libraries. The id is what a descent
/// carries. The caption is the line under every slot, the line is what the
/// focused slot shows, and `under` is the second caption line, empty on
/// every wall but a person's and the libraries strip. `episode` is what an
/// episode slot carries: the series a select opens and the numbers it
/// opens on, and an item that holds one draws as a still. `art_library` is
/// the library the art resolves against, where that is not the item's own:
/// a franchise slot opens in the `Library` of kind franchises, and its art
/// may be a member's poster on the member's own volume.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Item {
    pub library: String,
    pub art_library: String,
    pub kind: String,
    pub id: String,
    pub name: String,
    pub caption: String,
    pub line: facts::Line,
    pub under: String,
    pub art: String,
    pub episode: Option<InSeries>,
}

impl Item {
    /// One slot as an item. The query decides the caption: a person's works
    /// are read by year, so their caption carries it, and every other wall
    /// captions the name alone. An episode slot, whatever the query, is
    /// captioned with its numbers and its series' name, because the still
    /// alone does not say which series it is from, and its focused line is
    /// the same two, bright. A recency, genre, or set strip draws a second
    /// line under every slot, an episode's title and runtime or a title's
    /// year, runtime, and rating, because a strip's caption band holds one
    /// short line and an episode's title would otherwise be dropped from the
    /// end. A person's strip draws the parts there. Every line is built once
    /// here, at the read, and not on every frame.
    pub fn of(query: &Query, slot: Slot) -> Self {
        let year = facts::year(&slot.released);
        let numbers = slot
            .episode
            .as_ref()
            .map(|place| format!("S{:02}E{:02}", place.season, place.episode))
            .unwrap_or_default();
        let series = slot
            .episode
            .as_ref()
            .map(|place| place.name.as_str())
            .unwrap_or_default();
        let caption = match (&slot.episode, query) {
            (Some(_), _) => facts::joined(&[&numbers, series]),
            (None, Query::Person { .. }) => facts::joined(&[&slot.title, year]),
            (None, _) => slot.title.clone(),
        };
        let runtime = facts::runtime(slot.duration);
        let line = match slot.episode.is_some() {
            true => facts::Line::of(&[series, &numbers]),
            false => facts::Line::of(&[&slot.title, year, &runtime, &slot.rating]),
        };
        let under = match (query, &slot.episode) {
            (Query::Person { .. } | Query::Library { .. }, _) => slot.parts.clone(),
            (_, Some(_)) => facts::joined(&[&slot.title, &runtime]),
            (_, None) => facts::joined(&[year, &runtime, &slot.rating]),
        };
        Self {
            library: slot.library,
            art_library: String::new(),
            kind: slot.kind,
            id: slot.id,
            name: slot.title,
            caption,
            line,
            under,
            art: slot.art,
            episode: slot.episode,
        }
    }
}

impl Card for Item {
    fn art(&self) -> &str {
        &self.art
    }

    fn ratio(&self) -> f32 {
        match self.episode.is_some() {
            true => STILL,
            false => POSTER,
        }
    }

    // The art resolves against the library that holds it, which is the
    // item's own unless the item names another.
    fn library(&self) -> &str {
        match self.art_library.is_empty() {
            true => &self.library,
            false => &self.art_library,
        }
    }

    fn name(&self) -> &str {
        &self.name
    }

    fn caption(&self) -> &str {
        &self.caption
    }

    fn under(&self) -> &str {
        &self.under
    }

    fn line_fitting(&self, chars: usize) -> &str {
        self.line.fitting(chars)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Fold;

    const LIBRARY: &str = "sample/features";

    fn library() -> Query {
        Query::Library {
            library: LIBRARY.into(),
        }
    }

    fn specimen() -> Slot {
        Slot {
            library: LIBRARY.into(),
            kind: "movies".into(),
            id: "movie:sample:1".into(),
            title: "Specimen 0001".into(),
            released: "1987-04-02".into(),
            art: "posters/1.jpg".into(),
            duration: 5_820,
            rating: "PG-13".into(),
            parts: String::new(),
            episode: None,
        }
    }

    #[test]
    fn a_slot_carries_the_facts_its_row_holds() {
        let item = Item::of(&library(), specimen());
        assert_eq!(item.line.words(), "Specimen 0001 · 1987 · 1h 37m · PG-13");
        assert_eq!(item.name, "Specimen 0001");
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "");
        assert_eq!(item.art, "posters/1.jpg");
        assert_eq!(item.library(), LIBRARY);
        assert_eq!(item.kind, "movies");
        assert_eq!(item.ratio(), POSTER);
    }

    #[test]
    fn an_episode_is_captioned_with_its_numbers_and_its_series_and_draws_as_a_still() {
        let query = Query::Released {
            fold: crate::catalog::Fold::Airing,
        };
        let item = Item::of(
            &query,
            Slot {
                kind: "episodes".into(),
                id: "episode:sample:1".into(),
                title: "Segment 04".into(),
                released: "2026-09-01".into(),
                duration: 2_760,
                episode: Some(InSeries {
                    series: "series:sample:03".into(),
                    name: "Serial 03".into(),
                    season: 3,
                    episode: 4,
                }),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "S03E04 · Serial 03");
        assert_eq!(item.line.words(), "Serial 03 · S03E04");
        assert_eq!(item.under(), "Segment 04 · 46m");
        assert_eq!(item.ratio(), STILL);
        assert_eq!(item.kind, "episodes");
    }

    #[test]
    fn a_recency_slot_of_a_title_is_captioned_with_the_title() {
        let query = Query::Added {
            fold: crate::catalog::Fold::Titles,
        };
        assert_eq!(Item::of(&query, specimen()).caption(), "Specimen 0001");
    }

    #[test]
    fn a_persons_work_is_captioned_with_its_year_and_its_parts() {
        let query = Query::Person {
            library: LIBRARY.into(),
            path: ".contributors/A Player".into(),
        };
        let item = Item::of(
            &query,
            Slot {
                parts: "Director, as The Part".into(),
                duration: 0,
                rating: String::new(),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "Specimen 0001 · 1987");
        assert_eq!(item.line_fitting(80), "Specimen 0001 · 1987");
        assert_eq!(item.under(), "Director, as The Part");
    }

    #[test]
    fn a_title_on_a_recency_strip_carries_its_facts_under_it() {
        let item = Item::of(&Query::Added { fold: Fold::Airing }, specimen());
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "1987 · 1h 37m · PG-13");
    }

    #[test]
    fn a_title_on_a_genre_or_a_set_strip_carries_its_facts_under_it() {
        let genre = Query::Genre {
            name: "Western".into(),
            order: crate::catalog::Order::Released,
        };
        let item = Item::of(&genre, specimen());
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "1987 · 1h 37m · PG-13");
        let set = Query::Set {
            library: LIBRARY.into(),
            id: "set:sample:01".into(),
        };
        assert_eq!(Item::of(&set, specimen()).under(), "1987 · 1h 37m · PG-13");
    }

    #[test]
    fn a_narrow_band_drops_a_slot_s_facts_from_the_end() {
        let item = Item::of(&library(), specimen());
        assert_eq!(
            item.line_fitting(37),
            "Specimen 0001 · 1987 · 1h 37m · PG-13"
        );
        assert_eq!(item.line_fitting(36), "Specimen 0001 · 1987 · 1h 37m");
        assert_eq!(item.line_fitting(24), "Specimen 0001 · 1987");
        assert_eq!(item.line_fitting(19), "Specimen 0001");
        assert_eq!(item.line_fitting(4), "Specimen 0001");
    }

    #[test]
    fn a_slot_leaves_out_what_its_row_does_not_hold() {
        let item = Item::of(
            &library(),
            Slot {
                id: "movie:sample:2".into(),
                title: "Specimen 0002".into(),
                ..Slot::default()
            },
        );
        assert_eq!(item.line.words(), "Specimen 0002");
        assert_eq!(item.line_fitting(4), "Specimen 0002");
    }
}
