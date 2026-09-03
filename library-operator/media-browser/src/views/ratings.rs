// The ratings line: each site's own mark, then the score on that site's
// scale, in one row under the facts of a title. The marks are the sites'
// logos, baked into the binary, because a drawn imitation reads as a
// fake and a fetched one would need a network the screen does not have.

use std::sync::OnceLock;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_widget::core::Bytes;
use iced_widget::image::Handle;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point};

use super::{area, label, text};
use crate::look;

/// The height the line takes in the stack that holds it.
pub const HEIGHT: f32 = look::SCORE * text::LEADING;

// The height of one mark, a little under the line's own height so the
// marks sit inside it.
const MARK: f32 = 22.0;

// The space between a mark and the score beside it.
const GAP: f32 = 8.0;

// The space between one entry and the next.
const BETWEEN: f32 = 28.0;

// The space between a score and its scale, so the small face does not
// touch the large one.
const SCALE_GAP: f32 = 4.0;

// The name the sidecar's ratings block writes for each of the three
// sites the line draws. Jellyfin reads a name holding "tomato" as the
// critic rating, and tomatometerallcritics is the name it writes.
const IMDB: &str = "imdb";
const TOMATOMETER: &str = "tomatometerallcritics";
const METACRITIC: &str = "metacritic";

// The three marks, baked into the binary at 128 pixels tall, so the line
// draws the sites' own logos and asks no volume and no network for them.
// The renderer keys its uploads by handle id, so each mark decodes once
// and keeps its handle for the life of the process.
const IMDB_MARK: &[u8] = include_bytes!("../../assets/ratings/imdb.png");
const TOMATO_MARK: &[u8] = include_bytes!("../../assets/ratings/rotten-tomatoes.png");
const METACRITIC_MARK: &[u8] = include_bytes!("../../assets/ratings/metacritic.png");

static MARKS: OnceLock<[Option<Logo>; 3]> = OnceLock::new();

// One decoded mark: its handle, and the width it takes at the line's
// mark height, from its own ratio.
struct Logo {
    handle: Handle,
    width: f32,
}

impl Logo {
    fn decode(png: &[u8]) -> Option<Self> {
        let decoded = image::load_from_memory(png).ok()?.to_rgba8();
        let (width, height) = decoded.dimensions();
        let handle = Handle::from_rgba(width, height, Bytes::from(decoded.into_raw()));
        Some(Self {
            handle,
            width: MARK * width as f32 / height as f32,
        })
    }
}

fn logo(mark: Mark) -> Option<&'static Logo> {
    let marks = MARKS.get_or_init(|| {
        [
            Logo::decode(IMDB_MARK),
            Logo::decode(TOMATO_MARK),
            Logo::decode(METACRITIC_MARK),
        ]
    });
    marks[mark as usize].as_ref()
}

/// Which site's mark draws before a score.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mark {
    /// IMDb's yellow wordmark.
    Imdb,
    /// The tomatometer's tomato.
    Tomato,
    /// Metacritic's round mark.
    Metacritic,
}

/// One entry of the line: the site's mark, and its score on that site's
/// own scale.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Score {
    /// The site's mark.
    pub mark: Mark,
    /// The score, on the site's own scale.
    pub value: f64,
}

impl Score {
    /// The score in the site's own form: one decimal for IMDb, and a whole
    /// number for the tomatometer and the Metascore.
    pub fn shown(&self) -> String {
        match self.mark {
            Mark::Imdb => format!("{:.1}", self.value),
            Mark::Tomato | Mark::Metacritic => format!("{}", self.value.round()),
        }
    }

    /// The site's scale, which draws small and dim after the score: out
    /// of ten for IMDb, a percentage for the tomatometer, and out of a
    /// hundred for the Metascore.
    pub fn scale(&self) -> &'static str {
        match self.mark {
            Mark::Imdb => "/10",
            Mark::Tomato => "%",
            Mark::Metacritic => "/100",
        }
    }
}

/// The entries the line draws, in the one order it draws them: IMDb, the
/// tomatometer, then Metacritic. A site the body holds no score for is
/// left out, and TMDb's score is left off the line.
pub fn scores(ratings: &[(String, f64)]) -> Vec<Score> {
    [
        (Mark::Imdb, IMDB),
        (Mark::Tomato, TOMATOMETER),
        (Mark::Metacritic, METACRITIC),
    ]
    .into_iter()
    .filter_map(|(mark, name)| {
        let (_, value) = ratings.iter().find(|(held, _)| held == name)?;
        Some(Score {
            mark,
            value: *value,
        })
    })
    .collect()
}

/// Draw the line with its left edge at `at`. The answer is the height it
/// took, and zero where the title holds no score, so the caller stacks
/// the next block under it.
pub fn draw(frame: &mut canvas::Frame<Renderer>, scores: &[Score], at: Point) -> f32 {
    if scores.is_empty() {
        return 0.0;
    }
    let mut left = at.x;
    let top = at.y + (HEIGHT - MARK) / 2.0;
    for score in scores {
        left += entry(frame, *score, Point::new(left, top)) + BETWEEN;
    }
    HEIGHT
}

// One entry: the site's mark, then the score, then its scale. The answer
// is the width the entry took. A mark that did not decode leaves the
// score alone on the line.
fn entry(frame: &mut canvas::Frame<Renderer>, score: Score, at: Point) -> f32 {
    let width = match logo(score.mark) {
        Some(logo) => {
            frame.draw_image(
                area(at.x, at.y, logo.width, MARK),
                canvas::Image::new(logo.handle.clone()),
            );
            logo.width + GAP
        }
        None => 0.0,
    };
    let shown = beside(
        frame,
        Point::new(at.x + width, at.y),
        &score.shown(),
        look::SCORE,
        look::text(),
    );
    let scale = beside(
        frame,
        Point::new(at.x + width + shown + SCALE_GAP, at.y),
        score.scale(),
        look::CAPTION,
        look::faint(),
    );
    width + shown + SCALE_GAP + scale
}

// One run of text beside a mark, centred on the mark's height. The answer
// is the width it took.
fn beside(
    frame: &mut canvas::Frame<Renderer>,
    at: Point,
    content: &str,
    size: f32,
    color: Color,
) -> f32 {
    let width = text::width(content, size);
    let band = area(at.x, at.y, width, MARK);
    frame.fill_text(label(
        content,
        Point::new(band.x, band.center_y()),
        size,
        color,
        Alignment::Left,
        Vertical::Center,
        f32::INFINITY,
    ));
    width
}

#[cfg(test)]
mod tests {
    use super::*;

    fn held() -> Vec<(String, f64)> {
        [
            ("imdb", 6.5),
            ("metacritic", 80.0),
            ("themoviedb", 7.1),
            ("tomatometerallcritics", 83.0),
        ]
        .into_iter()
        .map(|(name, value)| (name.to_string(), value))
        .collect()
    }

    #[test]
    fn the_line_draws_three_sites_in_its_own_order_and_leaves_tmdb_off() {
        let scores = scores(&held());
        let marks: Vec<Mark> = scores.iter().map(|score| score.mark).collect();
        assert_eq!(marks, [Mark::Imdb, Mark::Tomato, Mark::Metacritic]);
        assert_eq!(scores[0].value, 6.5);
        assert_eq!(scores[2].value, 80.0);
    }

    #[test]
    fn a_score_reads_in_the_sites_own_form_with_its_scale_after_it() {
        let scores = scores(&held());
        let parts: Vec<(String, &str)> = scores
            .iter()
            .map(|score| (score.shown(), score.scale()))
            .collect();
        assert_eq!(
            parts,
            [
                ("6.5".to_string(), "/10"),
                ("83".to_string(), "%"),
                ("80".to_string(), "/100"),
            ]
        );
    }

    #[test]
    fn a_site_the_body_holds_no_score_for_is_left_out() {
        let held = vec![("tomatometerallcritics".to_string(), 83.0)];
        let scores = scores(&held);
        assert_eq!(scores.len(), 1);
        assert_eq!(scores[0].mark, Mark::Tomato);
    }

    #[test]
    fn a_title_with_no_ratings_draws_no_line() {
        assert!(scores(&[]).is_empty());
    }
}
