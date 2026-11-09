// The measure of a movie page: where every block lands, and how far the
// page has scrolled to hold the block that has focus.

use super::*;
use crate::catalog::stream::{Stream, Tracks};
use crate::catalog::{CreditSlot, Credits, FileFacts, Progress};
use crate::screens::movie::Set;
use crate::screens::stripes::Stripes;
use crate::screens::{Item, facts};
use crate::screens::{files, foot};
use crate::tracks::Preferences;

const WIDTH: f32 = 1920.0;
const HEIGHT: f32 = 1080.0;

// The frame every measure here is taken against.
const FRAME: Rectangle = Rectangle {
    x: 0.0,
    y: 0.0,
    width: WIDTH,
    height: HEIGHT,
};

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
        like: None,
        library: "screening/films".into(),
        id: "one".into(),
        title: "A Long Title That Wraps Onto Two".into(),
        logo: String::new(),
        backdrop: "backdrop.jpg".into(),
        trailer: true,
        duration: 6_730,
        facts: "1994 · 1h 52m · PG · Drama, Mystery".into(),
        ratings: ratings::scores(&[
            ("imdb".to_string(), 6.5),
            ("metacritic".to_string(), 80.0),
            ("tomatometerallcritics".to_string(), 83.0),
        ]),
        tagline: "One line of it, and a few more words after that.".into(),
        plot: "word ".repeat(120),
        stripes: stripes(),
        foot: foot::Foot::of(&["A Studio".to_string()], &[]),
        files: files::Files::of(
            &[(
                FileFacts {
                    path: "One/One.mkv".into(),
                    role: "primary".into(),
                    kind: "video".into(),
                    width: 1_920,
                    height: 804,
                    size_bytes: 4_200_000_000,
                    ..FileFacts::default()
                },
                Tracks {
                    streams: vec![Stream {
                        kind: "audio".into(),
                        codec: "ac3".into(),
                        language: "eng".into(),
                        ..Stream::default()
                    }],
                    beside: Vec::new(),
                },
            )],
            Some("One/One.mkv"),
            &Preferences::default(),
        ),
        progress: None,
        via: None,
        set: Some(Set {
            id: "set:films".into(),
            heading: "The Set".into(),
            members: vec![Item {
                art_library: String::new(),
                episode: None,
                library: "default/films".into(),
                kind: "movies".into(),
                id: "one".into(),
                name: "Film one".into(),
                released: "1999".into(),
                caption: "Film one".into(),
                fitted: "Film one".into(),
                line: facts::Line::of(&["Film one"]),
                under: String::new(),
                under_fitted: String::new(),
                tagline: false,
                art: String::new(),
                tiles: Vec::new(),
                new: 0,
                progress: None,
                reached: Default::default(),
                franchise: None,
            }],
            current: 0,
        }),
        focus,
        scroll: crate::motion::Glide::still(0.0),
        rows: crate::motion::Rows::default(),
    }
}

fn blocks(movie: &Movie) -> Blocks {
    Blocks::of(movie, FRAME)
}

// The top of the set strip, the block the button row's own block stands
// over.
fn strip_top(movie: &Movie) -> f32 {
    blocks(movie).strip.expect("the page draws a set strip").top
}

#[test]
fn the_status_line_takes_the_same_band_in_every_state() {
    let plain = crowded(Focus::Buttons(0));
    let mut reached = crowded(Focus::Buttons(0));
    reached.progress = Some(Progress {
        position: 900,
        duration: 6_720,
        ..Progress::default()
    });
    let finished = Movie {
        progress: Some(Progress {
            finished: true,
            ..Progress::default()
        }),
        ..crowded(Focus::Buttons(0))
    };

    assert_eq!(strip_top(&reached), strip_top(&plain));
    assert_eq!(strip_top(&finished), strip_top(&plain));
    assert_eq!(
        blocks(&plain).buttons.height,
        buttons::HEIGHT + line::height()
    );
}

#[test]
fn the_marks_show_the_page_from_its_top() {
    let movie = crowded(Focus::Marks(0));
    assert_eq!(blocks(&movie).scroll(&movie, HEIGHT), 0.0);
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
    movie.files = files::Files::default();
    let blocks = blocks(&movie);
    assert!(blocks.stripes.is_empty());
    assert_eq!(blocks.foot.height, 0.0);
    assert_eq!(blocks.files.height, 0.0);
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
    assert_eq!(
        blocks.foot.height,
        movie.foot.height(screen::region(FRAME).width)
    );
}

// A film in no set draws More like this under its last stripe, and the
// studios follow the strip.
#[test]
fn the_more_like_this_strip_follows_the_stripes_and_the_studios_follow_it() {
    let mut movie = crowded(Focus::Like(0));
    movie.set = None;
    movie.like = crate::screens::movie::alike::Like::of(vec![crate::catalog::Alike {
        slot: crate::catalog::Slot {
            library: "screening/films".into(),
            kind: "movies".into(),
            id: "two".into(),
            ..crate::catalog::Slot::default()
        },
        score: 3,
        rating: 0.0,
    }]);
    let blocks = blocks(&movie);
    let last = blocks.stripes.last().expect("the crowded page credits");
    let like = blocks.like.expect("the page draws the strip");
    assert_eq!(like.top, last.bottom() + GAP + STRIPE_LEAD);
    assert_eq!(like.height, strip::height(card::LINES));
    assert_eq!(blocks.foot.top, like.bottom() + GAP + STRIPE_LEAD);
    assert_eq!(blocks.after(*last), like.top);

    let offset = blocks.scroll(&movie, SHORT);
    assert!(like.bottom() - offset <= SHORT);
    let marked = movie.marked(area(0.0, 0.0, WIDTH, SHORT));
    assert!(marked.is_some_and(|around| around.y >= like.top - offset));
}

#[test]
fn the_files_section_follows_the_studios_and_ends_the_page() {
    let movie = crowded(Focus::Buttons(0));
    let blocks = blocks(&movie);
    assert_eq!(blocks.files.top, blocks.foot.bottom() + GAP + FILES_LEAD);
    assert_eq!(blocks.files.height, movie.files.height());
    assert_eq!(blocks.content, blocks.files.bottom() + FOOT);
}

#[test]
fn a_movie_with_studios_and_no_file_ends_at_its_studios() {
    let mut movie = crowded(Focus::Buttons(0));
    movie.files = files::Files::default();
    let blocks = blocks(&movie);
    assert_eq!(blocks.content, blocks.foot.bottom() + FOOT);
}

#[test]
fn the_last_stripe_pulls_the_foot_into_view() {
    let movie = crowded(Focus::Stripe(1, 0));
    let blocks = blocks(&movie);
    let offset = blocks.scroll(&movie, SHORT);
    assert!(blocks.foot.bottom() - offset <= SHORT);
}

// A studio on the foot is the last stop of the page, so the page scrolls
// to its end and the outline goes around the name where the foot draws
// it, on the screen.
#[test]
fn the_outline_goes_around_the_focused_studio_at_the_end_of_the_page() {
    let movie = crowded(Focus::Studio(0));
    let (blocks, offset) = placed(&movie, area(0.0, 0.0, WIDTH, SHORT));
    let region = screen::region(FRAME);
    let name = movie
        .foot
        .placed(Point::new(region.x, blocks.foot.top - offset), region.width)[0];
    assert_eq!(offset, blocks.content - SHORT);
    assert_eq!(
        movie.marked(area(0.0, 0.0, WIDTH, SHORT)),
        Some(crate::views::marked(name))
    );
    assert!(name.y + name.height <= SHORT);
}

#[test]
fn the_row_names_when_its_first_button_would_end() {
    use crate::catalog::Progress;
    use crate::clock::Time;
    let now = Time {
        hour: 20,
        minute: 0,
    };
    let mut movie = crowded(Focus::Buttons(0));
    assert_eq!(movie.ends(now), "ends 9:52 pm");
    movie.progress = Some(Progress {
        position: 3_600,
        duration: 6_730,
        ..Progress::default()
    });
    assert_eq!(movie.ends(now), "ends 8:52 pm");
    movie.duration = 0;
    movie.progress = None;
    assert_eq!(movie.ends(now), "");
}

#[test]
fn the_title_starts_on_the_top_margin_the_clock_hangs_from_at_any_height() {
    let movie = crowded(Focus::Buttons(0));
    for height in [HEIGHT, SHORT] {
        let frame = Rectangle { height, ..FRAME };
        assert_eq!(Blocks::of(&movie, frame).title.top, screen::MARGIN_Y);
    }
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

// A film with a start to resume and a trailer draws the widest row, and
// it fits in the text column at 1920 by 1080.
#[test]
fn the_widest_playback_row_fits_inside_the_text_column() {
    use crate::screens::movie::row::Button;
    let words = [Button::Resume, Button::StartOver, Button::Trailer].map(Button::word);
    let last = buttons::button(&words, 2, Point::ORIGIN);
    assert!(
        last.x + last.width <= column(FRAME),
        "{last:?} past {}",
        column(FRAME)
    );
}
