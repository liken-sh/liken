// The media browser: a stack of levels over a catalog source and a
// poster store. The libraries level is always the bottom of the stack, so
// back from it goes nowhere and the screen is never empty.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Element, Length, Theme};

use crate::catalog::Source;
use crate::focus;
use crate::harness::{Screen, Waker};
use crate::levels::{Fetch, Level, Shape, shapes_of};
use crate::look;
use crate::posters::Posters;
use crate::views::list::List;
use crate::views::wall::{self, Wall};

/// The browsing screen, generic over where its rows and its posters
/// come from, so one browser draws the sidecar's file, a test fixture, and
/// the sample the same way.
pub struct Browser<S: Source, P: Posters> {
    source: S,
    // The store is in a RefCell because a canvas program draws through
    // a shared reference while the store mutates its cache.
    posters: RefCell<P>,
    // The libraries level is a field of its own, so the type guarantees
    // a level to draw; the stack holds only descents.
    libraries: Level,
    stack: Vec<Level>,
}

impl<S: Source, P: Posters> Browser<S, P> {
    /// Open the browser on its first screen, the libraries.
    pub fn new(mut source: S, posters: P) -> Self {
        let libraries = Level::new(Shape::List, Fetch::Libraries, &mut source);
        Self {
            source,
            posters: RefCell::new(posters),
            libraries,
            stack: Vec::new(),
        }
    }

    fn top(&self) -> &Level {
        self.stack.last().unwrap_or(&self.libraries)
    }

    fn top_mut(&mut self) -> &mut Level {
        self.stack.last_mut().unwrap_or(&mut self.libraries)
    }

    fn reread_top(&mut self) {
        match self.stack.last_mut() {
            Some(top) => top.reread(&mut self.source),
            None => self.libraries.reread(&mut self.source),
        }
    }

    // A descent pushes the next level of the focused row, and the
    // next level is a fact of the kind's table: the shape at the next depth,
    // or nothing when the row is as deep as its kind goes.
    fn descend(&mut self) {
        let top = self.top();
        let Some(row) = top.rows.get(top.focus) else {
            return;
        };
        let next = match &top.fetch {
            Fetch::Libraries => Some((
                shapes_of(&row.kind)[0],
                Fetch::Titles {
                    library: row.id.clone(),
                    kind: row.kind.clone(),
                },
            )),
            Fetch::Titles { library, kind } => shapes_of(kind).get(1).map(|shape| {
                (
                    *shape,
                    Fetch::Seasons {
                        library: library.clone(),
                        kind: kind.clone(),
                        series: row.id.clone(),
                    },
                )
            }),
            Fetch::Seasons {
                library,
                kind,
                series,
            } => shapes_of(kind).get(2).map(|shape| {
                (
                    *shape,
                    Fetch::Episodes {
                        library: library.clone(),
                        series: series.clone(),
                        // Season rows mint their ids from the season
                        // number, so the parse cannot fail on rows this
                        // program built.
                        season: row.id.parse().unwrap_or(0),
                    },
                )
            }),
            Fetch::Episodes { .. } => None,
        };
        if let Some((shape, fetch)) = next {
            let level = Level::new(shape, fetch, &mut self.source);
            self.stack.push(level);
        }
    }

    // Back pops one descent and re-reads the level it uncovers,
    // because a change that landed while the level was covered was folded
    // into the level that was shown at the time and not into this one.
    fn back(&mut self) {
        if self.stack.pop().is_some() {
            self.reread_top();
        }
    }
}

impl<S: Source, P: Posters> Screen for Browser<S, P> {
    // Nothing on the screen emits a message; a remote's presses
    // arrive as keys, and the type says so.
    type Message = Infallible;

    fn background(&self) -> Color {
        look::BACKGROUND
    }

    fn key(&mut self, name: &str) {
        match name {
            "enter" => self.descend(),
            "escape" | "backspace" => self.back(),
            _ => {
                let top = self.top_mut();
                top.focus = match top.shape {
                    Shape::Wall => focus::wall(top.focus, top.rows.len(), wall::COLUMNS, name),
                    Shape::List => focus::list(top.focus, top.rows.len(), name),
                };
            }
        }
    }

    // A poster that landed changes the frame and not the rows, so a
    // delivery redraws what is already read and only a changed source
    // re-reads the level.
    fn pump(&mut self, _at: f64) -> bool {
        let delivered = self.posters.get_mut().delivered();
        if !self.source.changed() {
            return delivered;
        }
        self.reread_top();
        true
    }

    // Both the source and the poster store deliver on threads of
    // their own, so both take the handle that wakes the loop.
    fn wake_by(&mut self, wake: Waker) {
        self.source.wake_by(wake.clone());
        self.posters.get_mut().wake_by(wake);
    }

    fn tick(&mut self, _at: f64) {}

    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer> {
        let top = self.top();
        match top.shape {
            Shape::Wall => canvas(Wall {
                rows: &top.rows,
                focus: top.focus,
                library: top.fetch.library(),
                posters: &self.posters,
            })
            .width(Length::Fill)
            .height(Length::Fill)
            .into(),
            Shape::List => canvas(List {
                rows: &top.rows,
                focus: top.focus,
                library: top.fetch.library(),
                posters: &self.posters,
            })
            .width(Length::Fill)
            .height(Length::Fill)
            .into(),
        }
    }

    // Every view here is still until something changes it, and the
    // source wakes the loop itself, so an idle browser schedules nothing
    // and the loop waits on events.
    fn next_frame(&self, _at: f64) -> Option<f64> {
        None
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use iced_widget::image::Handle;

    use super::*;
    use crate::catalog::{EpisodeRow, LibraryEntry, Title};

    #[derive(Default)]
    struct Fake {
        movies: usize,
        changed: bool,
        woken: bool,
        calls: Vec<&'static str>,
    }

    impl Source for Fake {
        fn libraries(&mut self) -> Vec<LibraryEntry> {
            self.calls.push("libraries");
            vec![
                LibraryEntry {
                    library: "screening/films".into(),
                    kind: "movies".into(),
                    items: self.movies as u64,
                },
                LibraryEntry {
                    library: "screening/serials".into(),
                    kind: "series".into(),
                    items: 2,
                },
            ]
        }

        fn titles(&mut self, _library: &str, kind: &str) -> Vec<Title> {
            self.calls.push("titles");
            let count = if kind == "movies" { self.movies } else { 2 };
            (1..=count)
                .map(|number| Title {
                    id: format!("{kind}:{number}"),
                    title: format!("Entry {number}"),
                    released: "1980".into(),
                    art: String::new(),
                })
                .collect()
        }

        fn seasons(&mut self, _library: &str, _series: &str) -> Vec<i64> {
            self.calls.push("seasons");
            vec![1, 2]
        }

        fn episodes(&mut self, _library: &str, _series: &str, season: i64) -> Vec<EpisodeRow> {
            self.calls.push("episodes");
            vec![EpisodeRow {
                id: "e1".into(),
                title: "Segment 1".into(),
                season,
                episode: 1,
                art: String::new(),
            }]
        }

        fn changed(&mut self) -> bool {
            std::mem::take(&mut self.changed)
        }

        fn wake_by(&mut self, _wake: Waker) {
            self.woken = true;
        }
    }

    #[derive(Default)]
    struct NoPosters {
        delivers: bool,
    }

    impl Posters for NoPosters {
        fn poster(&mut self, _: &str, _: &str, _: u32, _: u32) -> Option<Handle> {
            None
        }

        fn delivered(&mut self) -> bool {
            std::mem::take(&mut self.delivers)
        }
    }

    fn browser(movies: usize) -> Browser<Fake, NoPosters> {
        Browser::new(
            Fake {
                movies,
                ..Fake::default()
            },
            NoPosters::default(),
        )
    }

    #[test]
    fn the_first_screen_lists_the_libraries() {
        let browser = browser(3);
        let top = browser.top();
        assert_eq!(top.shape, Shape::List);
        assert_eq!(top.rows[0].name, "films");
        assert_eq!(top.rows[0].detail, "movies · 3");
        assert_eq!(top.rows[1].name, "serials");
        assert_eq!(top.rows[1].detail, "series · 2");
    }

    #[test]
    fn enter_opens_a_movies_wall() {
        let mut browser = browser(3);
        browser.key("enter");
        assert_eq!(browser.top().shape, Shape::Wall);
        assert_eq!(browser.top().rows.len(), 3);
    }

    #[test]
    fn a_movie_is_as_deep_as_its_kind_goes() {
        let mut browser = browser(3);
        browser.key("enter");
        browser.key("enter");
        assert_eq!(browser.stack.len(), 1);
    }

    #[test]
    fn a_series_library_gives_three_lists() {
        let mut browser = browser(3);
        browser.key("down");
        browser.key("enter");
        assert_eq!(browser.top().shape, Shape::List);
        browser.key("enter");
        assert_eq!(browser.top().rows[0].name, "Season 1");
        browser.key("down");
        browser.key("enter");
        assert_eq!(browser.top().rows[0].detail, "S2 E1");
        browser.key("enter");
        assert_eq!(browser.stack.len(), 3);
    }

    #[test]
    fn back_climbs_one_level_and_stops_at_the_libraries() {
        let mut browser = browser(3);
        browser.key("enter");
        browser.key("escape");
        assert!(browser.stack.is_empty());
        browser.key("escape");
        assert!(browser.stack.is_empty());
        assert_eq!(browser.top().fetch, Fetch::Libraries);
    }

    #[test]
    fn backspace_goes_back_too() {
        let mut browser = browser(3);
        browser.key("enter");
        browser.key("backspace");
        assert!(browser.stack.is_empty());
    }

    #[test]
    fn back_rereads_the_level_it_uncovers() {
        let mut browser = browser(3);
        browser.key("enter");
        browser.key("escape");
        let libraries = browser
            .source
            .calls
            .iter()
            .filter(|call| **call == "libraries");
        assert_eq!(libraries.count(), 2);
    }

    #[test]
    fn arrows_move_focus_on_a_list_and_a_wall() {
        let mut browser = browser(20);
        browser.key("down");
        assert_eq!(browser.top().focus, 1);
        browser.key("up");
        browser.key("enter");
        browser.key("right");
        assert_eq!(browser.top().focus, 1);
        browser.key("down");
        assert_eq!(browser.top().focus, 1 + wall::COLUMNS);
    }

    #[test]
    fn an_empty_wall_selects_nothing() {
        let mut browser = browser(0);
        browser.key("enter");
        browser.key("enter");
        assert_eq!(browser.stack.len(), 1);
    }

    #[test]
    fn pump_without_a_change_folds_nothing() {
        let mut browser = browser(3);
        let reads = browser.source.calls.len();
        assert!(!browser.pump(1.0));
        assert_eq!(browser.source.calls.len(), reads);
    }

    #[test]
    fn pump_rereads_what_is_shown() {
        let mut browser = browser(3);
        browser.key("enter");
        browser.source.movies = 2;
        browser.source.changed = true;
        assert!(browser.pump(1.0));
        assert_eq!(browser.top().rows.len(), 2);
        assert_eq!(browser.source.calls.last(), Some(&"titles"));
        let libraries = browser
            .source
            .calls
            .iter()
            .filter(|call| **call == "libraries");
        assert_eq!(libraries.count(), 1);
    }

    #[test]
    fn a_delivered_poster_draws_a_frame_and_reads_no_rows() {
        let mut browser = browser(3);
        browser.key("enter");
        let reads = browser.source.calls.len();
        browser.posters.get_mut().delivers = true;
        assert!(browser.pump(1.0));
        assert_eq!(browser.source.calls.len(), reads);
        assert!(!browser.pump(2.0));
    }

    #[test]
    fn a_reread_that_shrinks_clamps_the_focus() {
        let mut browser = browser(3);
        browser.key("enter");
        browser.key("right");
        browser.key("right");
        browser.source.movies = 1;
        browser.source.changed = true;
        assert!(browser.pump(1.0));
        assert_eq!(browser.top().focus, 0);
    }

    #[test]
    fn the_wake_reaches_the_source() {
        let mut browser = browser(3);
        Screen::wake_by(&mut browser, Arc::new(|| {}));
        assert!(browser.source.woken);
    }

    #[test]
    fn a_still_browser_schedules_no_frame() {
        assert!(browser(3).next_frame(4.2).is_none());
    }

    #[test]
    fn the_browser_draws_on_the_theme_ground() {
        assert_eq!(browser(3).background(), look::BACKGROUND);
    }

    #[test]
    fn the_view_builds_for_both_shapes() {
        let mut browser = browser(3);
        let _ = browser.view();
        browser.key("enter");
        let _ = browser.view();
    }
}
