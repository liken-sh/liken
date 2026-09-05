// A kind is a plugin in the scanner and a screen design in the browser.
// This module holds the entries of the navigation stack: the home page
// at the bottom, and the screens each kind descends into. A screen holds
// the rows it read, decides what a press does, and composes the
// primitives in the views module. A new kind adds a screen here and,
// where it needs one, a primitive there. It adds no row to a table.

pub mod credits;
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

use self::series::seasons_of;
use crate::catalog::{InSeries, Query, Selection, Slot, Source};
use crate::posters::Posters;
use crate::views::curtain::Curtain;
use crate::views::{
    Card, card, strip,
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
/// carries. `episode` is what an
/// episode slot carries: the series a select opens and the numbers it
/// opens on, and an item that holds one draws as a still. `art_library` is
/// the library the art resolves against, where that is not the item's own:
/// a franchise slot opens in the `Library` of kind franchises, and its art
/// may be a member's poster on the member's own volume.
/// The caption is the words the card leads with, `under` is its second
/// line, and `line` is what the focused slot of a one-line wall shows.
/// `fitted` and `under_fitted` are those two lines cut by the shaper to
/// the band the card draws them in, so no frame measures them and no
/// line runs past its cell. A strip cuts every card at its read, and a
/// wall cuts the page of rows around its focus.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Item {
    pub library: String,
    pub art_library: String,
    pub kind: String,
    pub id: String,
    pub name: String,
    pub caption: String,
    pub fitted: String,
    pub line: facts::Line,
    pub under: String,
    pub under_fitted: String,
    /// Whether the card's first line is the film's tagline, which draws
    /// in the italic face, and not the title, which draws in the roman
    /// one.
    pub tagline: bool,
    pub art: String,
    /// The posters a shelf draws as a mosaic, each with the library it
    /// resolves against. Only a library and a genre carry any; every
    /// other item draws the one art beside it.
    pub tiles: Vec<(String, String)>,
    pub episode: Option<InSeries>,
    /// How many episodes of a folded show are current, and zero on every
    /// other item.
    pub new: usize,
}

impl Item {
    /// One slot as an item, with both caption lines built once here, at
    /// the read, and not on every frame.
    /// A still reads as its episode title over the series, the numbers,
    /// and the runtime. A work whose parts are all `as` runs reads as the
    /// character over the title and the year.
    /// Every other slot leads with the words its kind leads with, a
    /// film's tagline or a title.
    /// The second line under those words is one of four: the parts a
    /// person's strip credits with the year; the kind word with the year
    /// where that strip credits none; the year and the runtime on a set's
    /// strip, where every member is a film and the kind word says nothing;
    /// or the facts line: the year, a series' season count, the runtime,
    /// and the rating.
    pub fn of(query: &Query, slot: Slot) -> Self {
        let year = facts::year(&slot.released);
        let numbers = slot
            .episode
            .as_ref()
            .map(|place| format!("S{:02} · E{:02}", place.season, place.episode))
            .unwrap_or_default();
        let series = slot
            .episode
            .as_ref()
            .map(|place| place.name.as_str())
            .unwrap_or_default();
        let runtime = facts::runtime(slot.duration);
        let line = match slot.episode.is_some() {
            true => facts::Line::of(&[series, &numbers]),
            false => facts::Line::of(&[&slot.title, year, &runtime, &slot.rating]),
        };
        // The facts line. Only a series slot carries a season count, and
        // it stands between the year and the runtime.
        let facts = facts::joined(&[year, &seasons_of(slot.seasons), &runtime, &slot.rating]);
        // A whole show folded into one still reads the way one episode of
        // it does, so every still of a strip reads the same. The character
        // leads a work only where every part left is an `as` run.
        let leads = leading(&slot);
        let tagged = tagged(&slot);
        let (caption, under, tagline) = match (&slot.episode, query, played(&slot.parts)) {
            (Some(_), _, _) => (
                slot.title.clone(),
                facts::joined(&[series, &numbers, &runtime]),
                false,
            ),
            (None, Query::Person { .. }, Some(character)) => {
                (character, facts::joined(&[&slot.title, year]), false)
            }
            (None, Query::Person { .. }, None) => (
                leads,
                match slot.parts.is_empty() {
                    true => facts::joined(&[facts::kind_word(&slot.kind), year]),
                    false => facts::joined(&[&slot.parts, year]),
                },
                tagged,
            ),
            (None, Query::Set { .. }, _) => (leads, facts::joined(&[year, &runtime]), tagged),
            (None, _, _) => (leads, facts, tagged),
        };
        Self {
            library: slot.library,
            art_library: String::new(),
            kind: slot.kind,
            id: slot.id,
            name: slot.title,
            fitted: caption.clone(),
            caption,
            line,
            under_fitted: under.clone(),
            under,
            tagline,
            art: slot.art,
            tiles: Vec::new(),
            episode: slot.episode,
            new: slot.new,
        }
    }

    /// Both lines of the card cut by the shaper to a band of this width,
    /// each at the size it draws at, so the smaller second line keeps
    /// the words the first would have lost.
    pub fn fit(&mut self, band: f32) {
        self.fitted = card::cut(&self.caption, band);
        self.under_fitted = card::under_cut(&self.under, band);
    }
}

/// Every card of a strip, cut at the read to the band its own ratio
/// draws it in. A strip slot is one poster or one still wide, so the band
/// is a constant of the strip and not of the frame.
pub fn fitted_strip(items: &mut [Item]) {
    for item in items.iter_mut() {
        item.fit(strip::caption_width(item.ratio()));
    }
}

// The words a card leads with: a film's tagline where the sidecar wrote
// one, and the title everywhere else. A film's poster carries its title,
// so the card says something the poster cannot; a series is known by its
// name.
fn leading(slot: &Slot) -> String {
    match tagged(slot) {
        true => slot.tagline.clone(),
        false => slot.title.clone(),
    }
}

// Whether the words a card leads with are the film's tagline.
fn tagged(slot: &Slot) -> bool {
    slot.kind == MOVIES && !slot.tagline.is_empty()
}

// The kind a movie row carries, the one kind whose card leads with a
// tagline.
const MOVIES: &str = "movies";

// The run that names a character in a parts line, and never a role word.
const AS: &str = "as ";

// The characters of a parts line without the `as `, joined the way the
// parts were, and nothing where a part is not an `as` run, because then
// the parts line still says which role was which.
fn played(parts: &str) -> Option<String> {
    let runs: Vec<&str> = parts.split(", ").filter(|part| !part.is_empty()).collect();
    if runs.is_empty() || !runs.iter().all(|run| run.starts_with(AS)) {
        return None;
    }
    Some(
        runs.iter()
            .map(|run| &run[AS.len()..])
            .collect::<Vec<&str>>()
            .join(", "),
    )
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

    fn fitted(&self) -> &str {
        &self.fitted
    }

    fn under_fitted(&self) -> &str {
        &self.under_fitted
    }

    fn under(&self) -> &str {
        &self.under
    }

    fn line_fitting(&self, chars: usize) -> &str {
        self.line.fitting(chars)
    }

    fn leads_with_tagline(&self) -> bool {
        self.tagline
    }

    fn tiles(&self) -> &[(String, String)] {
        &self.tiles
    }

    fn new_episodes(&self) -> usize {
        self.new
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
            tagline: String::new(),
            parts: String::new(),
            episode: None,
            new: 0,
            seasons: 0,
        }
    }

    #[test]
    fn a_slot_carries_the_facts_its_row_holds() {
        let item = Item::of(&library(), specimen());
        assert_eq!(item.line.words(), "Specimen 0001 · 1987 · 1h 37m · PG-13");
        assert_eq!(item.name, "Specimen 0001");
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "1987 · 1h 37m · PG-13");
        assert_eq!(item.art, "posters/1.jpg");
        assert_eq!(item.library(), LIBRARY);
        assert_eq!(item.kind, "movies");
        assert_eq!(item.ratio(), POSTER);
    }

    #[test]
    fn an_episode_leads_with_its_own_title_over_its_show_and_draws_as_a_still() {
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
        assert_eq!(item.caption(), "Segment 04");
        assert_eq!(item.line.words(), "Serial 03 · S03 · E04");
        assert_eq!(item.under(), "Serial 03 · S03 · E04 · 46m");
        assert_eq!(item.ratio(), STILL);
        assert_eq!(item.kind, "episodes");
    }

    #[test]
    fn a_folded_show_reads_the_way_one_episode_of_it_does() {
        let query = Query::Released {
            fold: Fold::Shows { today: 0 },
        };
        let item = Item::of(
            &query,
            Slot {
                kind: "episodes".into(),
                id: "series:sample:03".into(),
                title: "Segment 08".into(),
                released: "2026-09-01".into(),
                duration: 3_120,
                new: 2,
                episode: Some(InSeries {
                    series: "series:sample:03".into(),
                    name: "Serial 03".into(),
                    season: 4,
                    episode: 8,
                }),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "Segment 08");
        assert_eq!(item.under(), "Serial 03 · S04 · E08 · 52m");
        assert_eq!(item.line.words(), "Serial 03 · S04 · E08");
        assert_eq!(item.ratio(), STILL);
        assert_eq!(item.new_episodes(), 2);
    }

    #[test]
    fn a_films_card_leads_with_its_tagline_and_keeps_its_title_as_its_name() {
        let item = Item::of(
            &library(),
            Slot {
                tagline: "One of a kind.".into(),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "One of a kind.");
        assert_eq!(item.under(), "1987 · 1h 37m · PG-13");
        assert_eq!(item.name(), "Specimen 0001");
        assert_eq!(item.line.words(), "Specimen 0001 · 1987 · 1h 37m · PG-13");
    }

    #[test]
    fn a_film_the_sidecar_wrote_no_tagline_for_leads_with_its_title() {
        assert_eq!(Item::of(&library(), specimen()).caption(), "Specimen 0001");
    }

    #[test]
    fn a_series_card_leads_with_its_title_whatever_its_tagline_says() {
        let item = Item::of(
            &library(),
            Slot {
                tagline: "One of a kind.".into(),
                ..serial()
            },
        );
        assert_eq!(item.caption(), "Serial 03");
        assert_eq!(item.under(), "2004 · 5 seasons · TV-14");
    }

    #[test]
    fn a_persons_card_of_a_film_leads_with_the_tagline_over_the_parts_and_the_year() {
        let item = Item::of(
            &person(),
            Slot {
                tagline: "One of a kind.".into(),
                parts: "Director, Writer".into(),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "One of a kind.");
        assert_eq!(item.under(), "Director, Writer · 1987");
    }

    #[test]
    fn a_card_cuts_both_its_lines_to_the_band_it_is_given() {
        let mut item = Item::of(
            &library(),
            Slot {
                title: "W".repeat(60),
                ..specimen()
            },
        );
        item.fit(200.0);
        assert!(item.fitted().ends_with('\u{2026}'));
        assert!(crate::views::text::measured(item.fitted(), crate::look::CAPTION) <= 200.0);
        assert_eq!(item.under_fitted(), item.under());
    }

    #[test]
    fn every_card_of_a_strip_is_cut_to_the_band_its_own_ratio_draws_in() {
        let mut items = vec![
            Item::of(
                &library(),
                Slot {
                    title: "W".repeat(60),
                    ..specimen()
                },
            ),
            Item::of(
                &library(),
                Slot {
                    title: "W".repeat(60),
                    episode: Some(InSeries::default()),
                    ..specimen()
                },
            ),
        ];
        fitted_strip(&mut items);
        let poster = crate::views::text::measured(items[0].fitted(), crate::look::CAPTION);
        let still = crate::views::text::measured(items[1].fitted(), crate::look::CAPTION);
        assert!(poster <= strip::caption_width(POSTER));
        assert!(still > poster);
    }

    #[test]
    fn a_recency_slot_of_a_title_is_captioned_with_the_title() {
        let query = Query::Added {
            fold: crate::catalog::Fold::Titles,
        };
        assert_eq!(Item::of(&query, specimen()).caption(), "Specimen 0001");
    }

    fn person() -> Query {
        Query::Person {
            library: LIBRARY.into(),
            path: ".contributors/A Player".into(),
        }
    }

    #[test]
    fn a_persons_work_is_captioned_with_its_title_over_its_parts_and_its_year() {
        let item = Item::of(
            &person(),
            Slot {
                parts: "Director, as The Part".into(),
                duration: 0,
                rating: String::new(),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.line_fitting(80), "Specimen 0001 · 1987");
        assert_eq!(item.under(), "Director, as The Part · 1987");
    }

    #[test]
    fn a_persons_work_with_no_parts_left_reads_its_kind_and_its_year() {
        let item = Item::of(
            &person(),
            Slot {
                parts: String::new(),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "Film · 1987");
    }

    #[test]
    fn a_title_on_a_recency_strip_carries_its_facts_under_it() {
        let item = Item::of(&Query::Added { fold: Fold::Airing }, specimen());
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "1987 · 1h 37m · PG-13");
    }

    // One serial as a slot of the strip that read it: five seasons, and
    // the columns a series row carries.
    fn serial() -> Slot {
        Slot {
            kind: "series".into(),
            id: "series:sample:03".into(),
            title: "Serial 03".into(),
            released: "2004-09-22".into(),
            duration: 0,
            rating: "TV-14".into(),
            seasons: 5,
            ..specimen()
        }
    }

    #[test]
    fn a_series_carries_its_season_count_between_its_year_and_its_rating() {
        let genre = Query::Genre {
            name: "Mystery".into(),
            order: crate::catalog::Order::Released,
        };
        let item = Item::of(&genre, serial());
        assert_eq!(item.caption(), "Serial 03");
        assert_eq!(item.under(), "2004 · 5 seasons · TV-14");
        assert_eq!(item.ratio(), POSTER);
    }

    #[test]
    fn a_series_of_one_season_and_a_series_of_none_read_in_their_own_words() {
        let genre = Query::Genre {
            name: "Mystery".into(),
            order: crate::catalog::Order::Released,
        };
        let one = Item::of(
            &genre,
            Slot {
                seasons: 1,
                ..serial()
            },
        );
        assert_eq!(one.under(), "2004 · 1 season · TV-14");
        let none = Item::of(
            &genre,
            Slot {
                seasons: 0,
                ..serial()
            },
        );
        assert_eq!(none.under(), "2004 · TV-14");
    }

    #[test]
    fn a_persons_card_of_a_series_leads_with_the_character_it_credits() {
        let item = Item::of(
            &person(),
            Slot {
                parts: "as The Part".into(),
                ..serial()
            },
        );
        assert_eq!(item.caption(), "The Part");
        assert_eq!(item.under(), "Serial 03 · 2004");
    }

    #[test]
    fn a_card_of_two_roles_keeps_its_title_over_the_parts_it_credits() {
        let item = Item::of(
            &person(),
            Slot {
                parts: "Director, as The Lead".into(),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "Director, as The Lead · 1987");
    }

    #[test]
    fn a_card_of_two_characters_leads_with_both_of_them() {
        let item = Item::of(
            &person(),
            Slot {
                parts: "as One, as Two".into(),
                ..specimen()
            },
        );
        assert_eq!(item.caption(), "One, Two");
        assert_eq!(item.under(), "Specimen 0001 · 1987");
    }

    #[test]
    fn a_persons_card_of_a_series_with_no_parts_left_reads_its_kind_and_its_year() {
        let item = Item::of(&person(), serial());
        assert_eq!(item.under(), "Series · 2004");
    }

    #[test]
    fn a_title_on_a_genre_strip_carries_its_facts_under_it() {
        let genre = Query::Genre {
            name: "Western".into(),
            order: crate::catalog::Order::Released,
        };
        let item = Item::of(&genre, specimen());
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "1987 · 1h 37m · PG-13");
    }

    #[test]
    fn a_film_on_a_set_strip_carries_its_year_and_its_runtime_under_it() {
        let set = Query::Set {
            library: LIBRARY.into(),
            id: "set:sample:01".into(),
        };
        let item = Item::of(&set, specimen());
        assert_eq!(item.caption(), "Specimen 0001");
        assert_eq!(item.under(), "1987 · 1h 37m");
    }

    #[test]
    fn a_card_that_leads_with_a_tagline_says_so_and_every_other_card_does_not() {
        let tagged = Slot {
            tagline: "One of a kind.".into(),
            ..specimen()
        };
        let item = Item::of(&library(), tagged);
        assert_eq!(item.caption(), "One of a kind.");
        assert!(item.leads_with_tagline());
        assert!(!Item::of(&library(), specimen()).leads_with_tagline());
        assert!(!Item::of(&library(), serial()).leads_with_tagline());
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
    fn a_slot_leaves_both_its_lines_as_the_words_they_are_until_a_strip_cuts_them() {
        let item = Item::of(
            &library(),
            Slot {
                title: "W".repeat(60),
                ..specimen()
            },
        );
        assert_eq!(item.fitted(), item.caption());
        assert_eq!(item.under_fitted(), item.under());
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
