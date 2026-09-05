// The measure of a movie page: where every block lands, and how far the
// page has scrolled to hold the block that has focus.

use super::*;
use crate::catalog::{CreditSlot, Credits, FileFacts};
use crate::screens::foot;
use crate::screens::movie::Set;
use crate::screens::stripes::Stripes;
use crate::screens::{Item, facts};

const WIDTH: f32 = 1920.0;
const HEIGHT: f32 = 1080.0;

// A 720p frame, which a crowded page is taller than, so the scroll
// has something to do.
const SHORT: f32 = 720.0;

// The two stripes of an invented title, so a crowded page carries
// every rung a press can reach.
fn stripes() -> Stripes {
    let slot = |name: &str| CreditSlot {
        name: name.to_string(),
        role: String::new(),
        contributor: format!(".contributors/{name}"),
        headshot: true,
    };
    Stripes::of(Credits {
        directors: vec![slot("A Director")],
        writers: vec![slot("A Writer")],
        cast: (1..=12)
            .map(|part| slot(&format!("Player {part}")))
            .collect(),
    })
}

// A page with everything on it: a title that wraps onto a second
// line, a tagline, a plot of four lines, a set strip, and three
// stripes of credited people.
fn crowded(focus: Focus) -> Movie {
    Movie {
        franchises: crate::screens::franchise::strips::Strips::default(),
        library: "screening/films".into(),
        id: "one".into(),
        title: "A Long Title That Wraps Onto Two".into(),
        logo: String::new(),
        backdrop: "backdrop.jpg".into(),
        trailer: true,
        facts: "1994 · 1h 52m · PG · Drama, Mystery".into(),
        ratings: ratings::scores(&[
            ("imdb".to_string(), 6.5),
            ("metacritic".to_string(), 80.0),
            ("tomatometerallcritics".to_string(), 83.0),
        ]),
        tagline: "One line of it, and a few more words after that.".into(),
        plot: "word ".repeat(120),
        stripes: stripes(),
        foot: foot::Foot::of(
            &["A Studio".to_string()],
            &[FileFacts {
                role: "primary".into(),
                kind: "video".into(),
                video_codec: "x265".into(),
                audio_codec: "AC3".into(),
                width: 1_920,
                height: 804,
                size_bytes: 4_200_000_000,
                ..FileFacts::default()
            }],
        ),
        set: Some(Set {
            heading: "The Set".into(),
            members: vec![Item {
                art_library: String::new(),
                episode: None,
                library: "default/films".into(),
                kind: "movies".into(),
                id: "one".into(),
                name: "Film one".into(),
                caption: "Film one".into(),
                fitted: "Film one".into(),
                line: facts::Line::of(&["Film one"]),
                under: String::new(),
                under_fitted: String::new(),
                tagline: false,
                art: String::new(),
                tiles: Vec::new(),
                new: 0,
            }],
            current: 0,
        }),
        focus,
    }
}

fn blocks(movie: &Movie) -> Blocks {
    Blocks::of(movie, WIDTH * COLUMN, WIDTH - 2.0 * MARGIN, HEIGHT * TOP)
}

#[test]
fn a_crowded_page_is_longer_than_a_short_frame() {
    assert!(blocks(&crowded(Focus::Buttons(0))).content > SHORT);
}

#[test]
fn the_focused_stripe_is_in_view_and_the_page_scrolls_to_reach_it() {
    let mut offsets = Vec::new();
    for stripe in 0..2 {
        let movie = crowded(Focus::Stripe(stripe, 0));
        let blocks = blocks(&movie);
        let offset = blocks.scroll(&movie, SHORT);
        let block = blocks.stripes[stripe];
        assert!(block.top - offset >= 0.0, "{block:?} at {offset}");
        assert!(block.bottom() - offset <= SHORT, "{block:?} at {offset}");
        offsets.push(offset);
    }
    assert!(offsets[0] > 0.0);
    assert!(offsets[1] > offsets[0]);
}

#[test]
fn the_strip_and_the_first_stripe_are_in_view_while_the_strip_has_focus() {
    let movie = crowded(Focus::Strip(0));
    let blocks = blocks(&movie);
    let offset = blocks.scroll(&movie, SHORT);
    let strip = blocks.strip.expect("the crowded page holds a set");
    assert!(strip.top - offset >= 0.0);
    assert!(strip.bottom() - offset <= SHORT);
    assert!(blocks.stripes[0].top - offset < SHORT);
    assert!(blocks.stripes[0].bottom() - offset > SHORT);
}

#[test]
fn the_set_strip_takes_the_height_of_a_strip_of_cards() {
    let blocks = blocks(&crowded(Focus::Strip(0)));
    let strip = blocks.strip.expect("the crowded page holds a set");
    assert_eq!(strip.height, strip::height(card::LINES));
    assert!(strip.height > strip::height(1));
}

#[test]
fn the_buttons_show_the_page_from_its_top() {
    let movie = crowded(Focus::Buttons(0));
    assert_eq!(blocks(&movie).scroll(&movie, HEIGHT), 0.0);
}

#[test]
fn a_page_with_no_set_puts_its_first_stripe_where_the_strip_was() {
    let mut movie = crowded(Focus::Buttons(0));
    movie.set = None;
    let blocks = blocks(&movie);
    assert!(blocks.strip.is_none());
    assert_eq!(
        blocks.stripes[0].top,
        blocks.buttons.bottom() + GAP + STRIPE_LEAD
    );
}

#[test]
fn every_stripe_stands_a_lead_under_the_block_over_it() {
    let movie = crowded(Focus::Buttons(0));
    let blocks = blocks(&movie);
    assert_eq!(
        blocks.stripes[1].top,
        blocks.stripes[0].bottom() + GAP + STRIPE_LEAD
    );
}

#[test]
fn a_movie_that_credits_nobody_and_holds_no_file_ends_at_its_strip() {
    let mut movie = crowded(Focus::Buttons(0));
    movie.stripes = Stripes::default();
    movie.foot = foot::Foot::default();
    let blocks = blocks(&movie);
    assert!(blocks.stripes.is_empty());
    assert_eq!(blocks.foot.height, 0.0);
    assert_eq!(
        blocks.content,
        blocks.strip.expect("the crowded page holds a set").bottom()
    );
}

#[test]
fn the_foot_follows_the_last_stripe_and_keeps_the_page_s_foot_margin() {
    let movie = crowded(Focus::Buttons(0));
    let blocks = blocks(&movie);
    let last = blocks.stripes.last().expect("the crowded page credits");
    assert_eq!(blocks.foot.top, last.bottom() + GAP + STRIPE_LEAD);
    assert_eq!(blocks.foot.height, movie.foot.height(WIDTH - 2.0 * MARGIN));
    assert_eq!(blocks.content, blocks.foot.bottom() + FOOT);
}

#[test]
fn the_last_stripe_pulls_the_foot_into_view() {
    let movie = crowded(Focus::Stripe(1, 0));
    let blocks = blocks(&movie);
    let offset = blocks.scroll(&movie, SHORT);
    assert!(blocks.foot.bottom() - offset <= SHORT);
}

#[test]
fn a_movie_with_a_logo_reserves_the_box_it_draws_in() {
    let mut movie = crowded(Focus::Buttons(0));
    movie.logo = "logo.png".into();
    assert_eq!(blocks(&movie).title.height, LOGO_HEIGHT);
}

#[test]
fn the_ratings_line_sits_under_the_facts_line() {
    let blocks = blocks(&crowded(Focus::Buttons(0)));
    assert_eq!(blocks.ratings.height, ratings::HEIGHT);
    assert_eq!(blocks.ratings.top, blocks.facts.bottom() + GAP);
    assert_eq!(blocks.tagline.top, blocks.ratings.bottom() + GAP);
}

#[test]
fn a_movie_with_no_score_takes_no_ratings_line() {
    let mut movie = crowded(Focus::Buttons(0));
    movie.ratings = Vec::new();
    let blocks = blocks(&movie);
    assert_eq!(blocks.ratings.height, 0.0);
    assert_eq!(blocks.tagline.top, blocks.ratings.top);
}

#[test]
fn a_line_the_movie_does_not_carry_takes_no_height_and_no_gap() {
    let mut movie = crowded(Focus::Buttons(0));
    movie.tagline = String::new();
    let blocks = blocks(&movie);
    assert_eq!(blocks.tagline.height, 0.0);
    assert_eq!(blocks.tagline.top, blocks.plot.top);
}
