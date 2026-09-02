// The series page over an invented catalog: the words it builds out of a
// body and a season of episodes, where a press takes focus across the
// dividers, and what a select asks the browser to play.

use super::*;
use crate::catalog::{
    Credit, LibraryEntry, MovieDetails, MovieSet, PlayItem, SeriesDetails, Title,
};
use crate::harness::Waker;

const SERIES: &str = "series:one";

// Three seasons of five, three, and six episodes, which fill two rows,
// one row, and two rows at four across.
const SEASONS: [i64; 3] = [5, 3, 6];

#[derive(Default)]
struct Serials {
    // Whether the series holds any episode at all. A library the scanner
    // has not reached looks like that.
    empty: bool,
    // How many episodes the last season holds, so a re-read can shorten
    // the wall under the focus.
    last: Option<i64>,
}

impl Source for Serials {
    fn libraries(&mut self) -> Vec<LibraryEntry> {
        Vec::new()
    }

    fn titles(&mut self, _library: &str, _kind: &str) -> Vec<Title> {
        Vec::new()
    }

    fn movie(&mut self, _library: &str, _id: &str) -> Option<MovieDetails> {
        None
    }

    fn set(&mut self, _library: &str, _id: &str) -> Option<MovieSet> {
        None
    }

    fn series(&mut self, _library: &str, id: &str) -> Option<SeriesDetails> {
        if id != SERIES {
            return None;
        }
        Some(SeriesDetails {
            title: "Serial One".into(),
            released: "2004".into(),
            rating: "TV-14".into(),
            tagline: "One line of it.".into(),
            plot: "The series' own plot.".into(),
            creators: vec!["A Creator".into()],
            cast: vec![Credit {
                name: "A Player".into(),
                role: "The Part".into(),
            }],
            backdrop: "backdrop.jpg".into(),
            seasons: SEASONS.len() as i64,
            ..SeriesDetails::default()
        })
    }

    fn episodes(&mut self, _library: &str, id: &str) -> Vec<Episode> {
        if id != SERIES || self.empty {
            return Vec::new();
        }
        let mut counts = SEASONS;
        if let Some(last) = self.last {
            counts[2] = last;
        }
        counts
            .iter()
            .enumerate()
            .flat_map(|(index, count)| {
                let season = index as i64 + 1;
                (1..=*count).map(move |episode| Episode {
                    season,
                    episode,
                    title: format!("Segment {episode}"),
                    released: format!("{}-03-0{episode}", 2003 + season),
                    duration: 2_760,
                    plot: format!("The plot of S{season} E{episode}."),
                    art: format!("s{season}e{episode}.jpg"),
                })
            })
            .collect()
    }

    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        Vec::new()
    }

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

fn page(serials: Serials) -> (Series, Serials) {
    let mut source = serials;
    let page =
        Series::open("screening/serials", SERIES, &mut source).expect("the catalog holds it");
    (page, source)
}

// One press on a page, with the source it reads from.
fn pressed(page: &mut Series, source: &mut Serials, key: &str) -> usize {
    page.key(key, source);
    page.focus
}

#[test]
fn a_page_opens_on_the_first_episode_of_the_first_season() {
    let (page, _) = page(Serials::default());
    assert_eq!(page.focus, 0);
    assert_eq!(page.title, "Serial One");
    assert_eq!(page.facts, "2004 · 3 seasons · TV-14");
    assert_eq!(page.tagline, "One line of it.");
    assert_eq!(page.cast, "A Player as The Part");
    assert_eq!(page.stills.len(), 14);
}

#[test]
fn a_series_the_library_does_not_hold_has_no_page() {
    let mut source = Serials::default();
    assert!(Series::open("screening/serials", "series:gone", &mut source).is_none());
}

#[test]
fn every_season_gets_a_divider_with_its_name_and_its_first_year() {
    let (page, _) = page(Serials::default());
    let names: Vec<&str> = page
        .seasons
        .iter()
        .map(|season| season.name.as_str())
        .collect();
    assert_eq!(names, ["Season 1", "Season 2", "Season 3"]);
    let years: Vec<&str> = page
        .seasons
        .iter()
        .map(|season| season.year.as_str())
        .collect();
    assert_eq!(years, ["2004", "2005", "2006"]);
    assert_eq!(page.seasons[1].run, Run { first: 5, count: 3 });
}

#[test]
fn a_still_carries_its_caption_and_the_facts_the_header_draws() {
    let (page, _) = page(Serials::default());
    assert_eq!(page.stills[1].caption, "E2 · Segment 2");
    assert_eq!(page.stills[1].line.words(), "E2 · Segment 2 · 46m");
    assert_eq!(page.stills[1].facts, "S1 E2 · Segment 2 · 46m");
    assert_eq!(page.stills[1].plot, "The plot of S1 E2.");
    assert_eq!(page.stills[1].art, "s1e2.jpg");
}

#[test]
fn a_narrow_band_drops_a_still_s_runtime_before_its_name() {
    let (page, _) = page(Serials::default());
    let still = &page.stills[1];
    assert_eq!(still.line_fitting(20), "E2 · Segment 2 · 46m");
    assert_eq!(still.line_fitting(19), "E2 · Segment 2");
    assert_eq!(still.line_fitting(4), "E2 · Segment 2");
}

#[test]
fn left_and_right_stay_inside_one_season() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(pressed(&mut page, &mut source, "left"), 0);
    page.focus = 4;
    assert_eq!(pressed(&mut page, &mut source, "right"), 4);
    page.focus = 5;
    assert_eq!(pressed(&mut page, &mut source, "left"), 5);
    assert_eq!(pressed(&mut page, &mut source, "right"), 6);
}

#[test]
fn down_and_up_cross_the_dividers() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(pressed(&mut page, &mut source, "down"), 4);
    assert_eq!(pressed(&mut page, &mut source, "down"), 5);
    assert_eq!(pressed(&mut page, &mut source, "down"), 8);
    assert_eq!(pressed(&mut page, &mut source, "up"), 5);
    assert_eq!(pressed(&mut page, &mut source, "up"), 4);
}

#[test]
fn the_first_and_the_last_row_of_a_page_hold_focus() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(pressed(&mut page, &mut source, "up"), 0);
    page.focus = 13;
    assert_eq!(pressed(&mut page, &mut source, "down"), 13);
}

#[test]
fn the_header_shows_the_focused_episodes_plot_in_place_of_the_series() {
    let (mut page, mut source) = page(Serials::default());
    assert_eq!(page.plot, "The series' own plot.");
    pressed(&mut page, &mut source, "down");
    let focused = &page.stills[page.focus];
    assert_eq!(focused.facts, "S1 E5 · Segment 5 · 46m");
    assert_eq!(focused.plot, "The plot of S1 E5.");
}

#[test]
fn select_plays_the_episode_and_the_rest_of_its_season() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = 6;
    let Step::Play { library, selection } = page.key("enter", &mut source) else {
        panic!("a select on a still plays it");
    };
    assert_eq!(library, "screening/serials");
    assert_eq!(
        selection,
        Selection::Episode {
            series: SERIES.into(),
            season: 2,
            episode: 2,
        }
    );
}

#[test]
fn a_series_with_no_episodes_draws_its_own_plot_and_plays_nothing() {
    let (page, _) = page(Serials {
        empty: true,
        ..Serials::default()
    });
    assert!(page.stills.is_empty());
    assert!(page.seasons.is_empty());
    assert_eq!(page.plot, "The series' own plot.");

    let (mut page, mut source) = (page, Serials::default());
    source.empty = true;
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    assert_eq!(pressed(&mut page, &mut source, "down"), 0);
}

#[test]
fn a_reread_that_shortens_the_wall_clamps_the_focus() {
    let (mut page, mut source) = page(Serials::default());
    page.focus = 13;
    source.last = Some(1);
    page.reread(&mut source);
    assert_eq!(page.stills.len(), 9);
    assert_eq!(page.focus, 8);
}

#[test]
fn a_reread_of_a_series_that_left_the_library_keeps_the_page() {
    let (mut page, _) = page(Serials::default());
    let mut gone = Serials::default();
    page.id = "series:gone".into();
    page.reread(&mut gone);
    assert_eq!(page.stills.len(), 14);
}

#[test]
fn a_series_whose_episodes_have_not_landed_names_no_seasons() {
    assert_eq!(seasons_of(0), "");
    assert_eq!(seasons_of(1), "1 season");
    assert_eq!(seasons_of(4), "4 seasons");
}
