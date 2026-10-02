//! The cast row: the credited people on screen when the film pauses. The
//! library's appearances fact writes a spans file for the item, and the row
//! reads the span the playhead stands in and draws one card per person, left
//! to right in the order the people stand in the picture. The display does no
//! matching and no ordering of its own: the file holds finished spans.
//!
//! Each card shows the portrait, the actor's name, the character's name in
//! italic and faint the way the library's browser shows it, and a line of
//! ages: how old the person was when the film came out, and how old they are
//! today or the year they died. The file holds dates and not ages, so the
//! row works out each age against the release date and the wall clock.
//!
//! The row is a focus stop above the up-next offer. Up reaches it, and left
//! and right move along it. A span with more people than the screen holds
//! runs off the right edge, and the row slides to keep the focused card on
//! screen.

use std::cell::Cell;
use std::collections::HashMap;

use iced::{Point, Rectangle, Size};
use jiff::civil::Date;
use serde_json::Value;

use crate::art::Art;
use crate::canvas::{Anchor, Brush, Canvas, Line, clip};
use crate::film::Film;
use crate::header::BETWEEN;
use crate::theme;
use crate::upnext::{CARD_ALPHA, CARD_R};

/// The portrait's box, which the decode fills.
pub const PORTRAIT_W: f32 = 57.0;
pub const PORTRAIT_H: f32 = 76.0;

/// The card's type sizes. The row shows while the film plays, so the cards
/// draw a step under the bar's own type and cover less of the picture.
const NAME_SIZE: f32 = 30.0;
const LINE_SIZE: f32 = 24.0;

/// One card's box and the pitch between two cards. The card is wide enough
/// for a long name and the ages line at the type sizes it draws in.
const CARD_W: f32 = 330.0;
const CARD_H: f32 = PORTRAIT_H + 2.0 * PAD;
const GAP: f32 = 14.0;
const PAD: f32 = 8.0;
const TEXT_GAP: f32 = 12.0;

/// The row's top edge and its heading. The cards end above the row where the
/// skip control and the up-next chip draw, so the row never moves when one
/// of them comes or goes.
const ROW_TOP: f32 = 688.0;
const HEADING_Y: f32 = 652.0;
const HEADING: &str = "IN THIS SCENE";

/// The drop from the card's top to each of its three lines.
const NAME_Y: f32 = 5.0;
const CHARACTER_Y: f32 = 34.0;
const AGES_Y: f32 = 58.0;

/// While the up-next card stands, the row ends this far short of it.
pub const CARD_CLEAR: f32 = 24.0;

/// The card that runs off the right edge draws at this share of the fade, so
/// the row shows that more people wait to the right.
const PEEK: f32 = 0.35;

/// The format the appearances fact writes.
const FORMAT: &str = "liken.sh/appearances/spans/v1";

/// One person as a card shows them.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Person {
    pub name: String,
    pub character: Option<String>,
    /// The portrait's path under the library's .contributors directory.
    pub portrait: Option<String>,
    pub born: Option<Date>,
    pub died: Option<Date>,
}

/// One span: its seconds and its people, left to right.
#[derive(Debug, Clone, PartialEq)]
struct Span {
    start: f64,
    end: f64,
    people: Vec<String>,
}

/// One item's spans file, as the display reads it.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Spans {
    released: Option<Date>,
    people: HashMap<String, Person>,
    spans: Vec<Span>,
}

impl Spans {
    /// Read one spans file. A file of another format, or text that does not
    /// parse, gives nothing, and the item shows no row.
    pub fn parse(text: &str) -> Option<Self> {
        let value: Value = serde_json::from_str(text).ok()?;
        if value.get("format")?.as_str()? != FORMAT {
            return None;
        }
        let date = |field: &Value, name: &str| field.get(name)?.as_str()?.parse::<Date>().ok();
        let word = |field: &Value, name: &str| {
            field
                .get(name)?
                .as_str()
                .filter(|word| !word.is_empty())
                .map(str::to_string)
        };
        let people = value
            .get("people")?
            .as_object()?
            .iter()
            .map(|(key, person)| {
                let card = Person {
                    name: word(person, "name").unwrap_or_default(),
                    character: word(person, "character"),
                    portrait: word(person, "portrait"),
                    born: date(person, "born"),
                    died: date(person, "died"),
                };
                (key.clone(), card)
            })
            .collect();
        let spans = value
            .get("spans")?
            .as_array()?
            .iter()
            .filter_map(|span| {
                Some(Span {
                    start: span.get("start")?.as_f64()?,
                    end: span.get("end")?.as_f64()?,
                    people: span
                        .get("people")?
                        .as_array()?
                        .iter()
                        .filter_map(|key| key.as_str().map(str::to_string))
                        .collect(),
                })
            })
            .collect();
        Some(Self {
            released: date(&value, "released"),
            people,
            spans,
        })
    }

    /// Read the file at one path. This runs on the blocking pool, because the
    /// path is on the library's volume and a read over the network must
    /// never hold up a frame.
    pub fn read(path: &str) -> Option<Self> {
        let text = std::fs::read_to_string(path).ok();
        if text.is_none() {
            eprintln!("media-display: no spans file at {path}");
        }
        Self::parse(&text?)
    }

    /// The span the second falls in. The spans are in time order and do not
    /// overlap, and a second between two spans names nobody.
    fn at(&self, second: f64) -> Option<usize> {
        let index = self.spans.partition_point(|span| span.end <= second);
        self.spans
            .get(index)
            .filter(|span| span.start <= second)
            .map(|_| index)
    }
}

/// The row's state: the item's spans, the library's .contributors
/// directory, and the focus within the row.
#[derive(Debug, Default)]
pub struct Cast {
    /// How many items have asked for a spans file, so a read the item swap
    /// outran lands on nothing.
    item: u64,
    spans: Option<Spans>,
    contributors: Option<String>,
    /// The span the focus belongs to. Playback or a seek moves into another
    /// span, and the focus starts again at its first card.
    span: Option<usize>,
    /// The focused card, and the first card the row shows.
    focus: usize,
    first: usize,
    /// How many whole cards the last frame had room for, which a press reads
    /// to know when the row has to slide.
    room: Cell<usize>,
}

impl Cast {
    /// A new item drops the last item's spans. It returns the file to read
    /// and the count the answer must carry, or nothing for an item with no
    /// spans file.
    pub fn on_item(
        &mut self,
        appearances: Option<&str>,
        contributors: Option<&str>,
    ) -> Option<(u64, String)> {
        self.item += 1;
        self.spans = None;
        self.contributors = contributors.map(str::to_string);
        self.span = None;
        self.focus = 0;
        self.first = 0;
        Some((self.item, appearances?.to_string()))
    }

    /// The read came back. It returns whether it landed.
    pub fn on_read(&mut self, item: u64, spans: Option<Spans>) -> bool {
        if item != self.item {
            return false;
        }
        self.spans = spans;
        true
    }

    /// The people of the span the playhead stands in, left to right.
    pub fn people(&self, film: &Film) -> Vec<&Person> {
        let Some((spans, index)) = self.span_at(film) else {
            return Vec::new();
        };
        spans.spans[index]
            .people
            .iter()
            .filter_map(|key| spans.people.get(key))
            .collect()
    }

    fn span_at(&self, film: &Film) -> Option<(&Spans, usize)> {
        let spans = self.spans.as_ref()?;
        Some((spans, spans.at(film.position?)?))
    }

    /// The row shows while the playhead stands in a span that names
    /// someone, playing or paused, so the display shows who is on screen
    /// whenever it is up.
    pub fn available(&self, film: &Film) -> bool {
        !self.people(film).is_empty()
    }

    /// Move the focus one card left or right. The focus stops at both ends,
    /// and the row slides only when the focus leaves the cards on screen.
    pub fn step(&mut self, direction: i64, film: &Film) {
        let count = self.people(film).len();
        if count == 0 {
            return;
        }
        let span = self.span_at(film).map(|(_, index)| index);
        if span != self.span {
            self.span = span;
            self.focus = 0;
            self.first = 0;
        }
        self.focus = (self.focus as i64 + direction).clamp(0, count as i64 - 1) as usize;
        let room = self.room.get().max(1);
        if self.focus < self.first {
            self.first = self.focus;
        } else if self.focus >= self.first + room {
            self.first = self.focus + 1 - room;
        }
    }

    /// The focused card and the first card shown, for the span the playhead
    /// stands in. A span the focus does not belong to starts at its first
    /// card.
    fn place(&self, film: &Film) -> (usize, usize) {
        match self.span_at(film).map(|(_, index)| index) == self.span {
            true => (self.focus, self.first),
            false => (0, 0),
        }
    }

    /// The portraits the row draws now, as paths the display opens.
    pub fn portraits(&self, film: &Film) -> Vec<String> {
        self.people(film)
            .into_iter()
            .filter_map(|person| self.portrait_path(person))
            .collect()
    }

    fn portrait_path(&self, person: &Person) -> Option<String> {
        let directory = self.contributors.as_deref()?.trim_end_matches('/');
        Some(format!("{directory}/{}", person.portrait.as_deref()?))
    }

    /// Draw the heading and the cards. `limit` is where the row must end:
    /// the up-next card's left edge less a clearance while it stands, and
    /// nothing otherwise, so the cards run off the right edge of the screen.
    pub fn draw(
        &self,
        brush: &mut Brush<'_>,
        film: &Film,
        art: &Art,
        focused: bool,
        limit: Option<f32>,
        today: Date,
    ) {
        let people = self.people(film);
        if people.is_empty() {
            return;
        }
        let canvas = brush.canvas();
        let end = limit.unwrap_or(canvas.width());
        let room = (((end - theme::MARGIN_X + GAP) / (CARD_W + GAP)).floor() as usize).max(1);
        self.room.set(room);
        let (focus, first) = self.place(film);
        let released = self.spans.as_ref().and_then(|spans| spans.released);

        brush.text(Line::new(
            HEADING,
            Point::new(theme::MARGIN_X, HEADING_Y),
            Anchor::TopLeft,
            theme::type_scale::TINY,
            theme::color::muted(),
        ));
        for (place, person) in people.iter().enumerate().skip(first) {
            let x = theme::MARGIN_X + (place - first) as f32 * (CARD_W + GAP);
            if x >= end {
                break;
            }
            let card = Rectangle::new(Point::new(x, ROW_TOP), Size::new(CARD_W, CARD_H));
            let whole = x + CARD_W <= end;
            // The card the edge cuts is the hint that the row goes on. It
            // draws only at the screen's own edge, so it never draws under
            // the up-next card.
            if !whole && limit.is_some() {
                break;
            }
            let fade = match whole {
                true => brush.fade(),
                false => brush.fade() * PEEK,
            };
            let portrait = self
                .portrait_path(person)
                .and_then(|path| art.portrait(&path));
            brush.at_fade(fade, |brush| {
                draw_card(
                    brush,
                    &canvas,
                    card,
                    person,
                    portrait,
                    focused && place == focus,
                    released,
                    today,
                );
            });
        }
    }
}

#[allow(clippy::too_many_arguments)]
fn draw_card(
    brush: &mut Brush<'_>,
    canvas: &Canvas,
    card: Rectangle,
    person: &Person,
    portrait: Option<&crate::art::Bitmap>,
    focused: bool,
    released: Option<Date>,
    today: Date,
) {
    if focused {
        brush.panel(card);
    } else {
        brush.rounded(card, CARD_R, theme::at(theme::color::SHADOW, CARD_ALPHA));
    }
    let picture = Point::new(canvas.snap(card.x + PAD), canvas.snap(card.y + PAD));
    match portrait {
        Some(bitmap) => bitmap.draw(brush, picture),
        None => brush.rounded(
            Rectangle::new(picture, Size::new(PORTRAIT_W, PORTRAIT_H)),
            6.0,
            theme::at(theme::color::muted(), theme::alpha::HIGHLIGHT),
        ),
    }
    let x = card.x + PAD + PORTRAIT_W + TEXT_GAP;
    let room = card.x + CARD_W - PAD - x;
    let line = |content: &str, drop: f32, size: f32, color| {
        Line::new(
            clip(content, size, room),
            Point::new(x, card.y + drop),
            Anchor::TopLeft,
            size,
            color,
        )
    };
    brush.text(line(&person.name, NAME_Y, NAME_SIZE, theme::color::text()));
    if let Some(character) = &person.character {
        brush.text(line(character, CHARACTER_Y, LINE_SIZE, theme::color::muted()).italic());
    }
    if let Some(ages) = ages(person, released, today) {
        brush.text(line(&ages, AGES_Y, LINE_SIZE, theme::color::muted()));
    }
}

/// The ages line: the age at the release, then the age today or the year of
/// death. A part with no date to work from is left out, and a person with no
/// dates at all has no line.
pub fn ages(person: &Person, released: Option<Date>, today: Date) -> Option<String> {
    let mut parts = Vec::new();
    if let (Some(born), Some(released)) = (person.born, released)
        && released >= born
    {
        parts.push(format!("{} in this film", years(born, released)));
    }
    match (person.born, person.died) {
        (_, Some(died)) => parts.push(format!("passed in {}", died.year())),
        (Some(born), None) => parts.push(format!("{} today", years(born, today))),
        (None, None) => {}
    }
    (!parts.is_empty()).then(|| parts.join(BETWEEN))
}

/// Whole years from one date to a later one: a birthday not yet reached in
/// the later year does not count.
fn years(from: Date, to: Date) -> i16 {
    let before = (to.month(), to.day()) < (from.month(), from.day());
    to.year() - from.year() - i16::from(before)
}

#[cfg(test)]
mod tests {
    use super::*;
    use jiff::civil::date;
    use serde_json::json;

    /// The spans file of a film with three spans: Jane and John together,
    /// a gap that names nobody, and four people in one shot.
    fn file() -> String {
        json!({
            "format": FORMAT,
            "released": "2009-10-10",
            "people": {
                "ja/jane": {"name": "Jane Roe", "character": "The Pilot",
                            "portrait": "ja/jane/headshot.jpg", "born": "1966-06-28"},
                "jo/john": {"name": "John Doe", "born": "1934-02-13", "died": "2021-03-23"},
                "a/a": {"name": "A"}, "b/b": {"name": "B"},
            },
            "spans": [
                {"start": 10.0, "end": 20.0, "people": ["jo/john", "ja/jane"]},
                {"start": 30.0, "end": 40.0, "people": ["ja/jane", "jo/john", "a/a", "b/b"]},
            ],
        })
        .to_string()
    }

    fn cast() -> Cast {
        let mut cast = Cast::default();
        let (item, _) = cast
            .on_item(Some("/media/1/spans.json"), Some("/media/1/.contributors/"))
            .unwrap();
        assert!(cast.on_read(item, Spans::parse(&file())));
        cast
    }

    fn paused_at(second: f64) -> Film {
        Film {
            position: Some(second),
            paused: true,
            ..Film::default()
        }
    }

    fn names(cast: &Cast, film: &Film) -> Vec<String> {
        cast.people(film).iter().map(|p| p.name.clone()).collect()
    }

    #[test]
    fn the_row_names_the_span_the_playhead_stands_in_left_to_right() {
        let cast = cast();
        assert_eq!(names(&cast, &paused_at(12.0)), vec!["John Doe", "Jane Roe"]);
        assert!(names(&cast, &paused_at(25.0)).is_empty());
        assert!(names(&cast, &paused_at(40.0)).is_empty());
        assert_eq!(names(&cast, &paused_at(30.0)).len(), 4);
    }

    #[test]
    fn the_row_is_a_stop_only_in_a_span_playing_or_paused() {
        let cast = cast();
        assert!(cast.available(&paused_at(12.0)));
        assert!(!cast.available(&paused_at(25.0)));
        let playing = Film {
            paused: false,
            ..paused_at(12.0)
        };
        assert!(cast.available(&playing));
        assert!(!Cast::default().available(&paused_at(12.0)));
    }

    #[test]
    fn a_read_the_item_swap_outran_lands_on_nothing() {
        let mut cast = Cast::default();
        let (stale, _) = cast.on_item(Some("a"), None).unwrap();
        cast.on_item(Some("b"), None);
        assert!(!cast.on_read(stale, Spans::parse(&file())));
        assert!(names(&cast, &paused_at(12.0)).is_empty());
        assert_eq!(Cast::default().on_item(None, None), None);
    }

    #[test]
    fn a_portrait_opens_under_the_contributors_directory() {
        let cast = cast();
        assert_eq!(
            cast.portraits(&paused_at(12.0)),
            vec!["/media/1/.contributors/ja/jane/headshot.jpg"]
        );
    }

    #[test]
    fn a_file_of_another_format_shows_no_row() {
        assert_eq!(
            Spans::parse(r#"{"format":"other","people":{},"spans":[]}"#),
            None
        );
        assert_eq!(Spans::parse("not json"), None);
    }

    #[test]
    fn the_focus_moves_along_the_row_and_the_row_slides_to_keep_it() {
        let mut cast = cast();
        let film = paused_at(35.0);
        cast.room.set(2);
        for _ in 0..3 {
            cast.step(1, &film);
        }
        assert_eq!(cast.place(&film), (3, 2));
        cast.step(1, &film);
        assert_eq!(cast.place(&film), (3, 2));
        cast.step(-1, &film);
        assert_eq!(cast.place(&film), (2, 2));
        cast.step(-1, &film);
        assert_eq!(cast.place(&film), (1, 1));
        // Another span starts the focus at its first card.
        assert_eq!(cast.place(&paused_at(12.0)), (0, 0));
    }

    #[test]
    fn the_ages_line_reads_the_release_today_and_a_death() {
        let today = date(2026, 10, 1);
        let released = Some(date(2009, 10, 10));
        let jane = Person {
            born: Some(date(1966, 6, 28)),
            ..Person::default()
        };
        assert_eq!(
            ages(&jane, released, today).as_deref(),
            Some("43 in this film  \u{00B7}  60 today")
        );
        let john = Person {
            born: Some(date(1934, 2, 13)),
            died: Some(date(2021, 3, 23)),
            ..Person::default()
        };
        assert_eq!(
            ages(&john, released, today).as_deref(),
            Some("75 in this film  \u{00B7}  passed in 2021")
        );
        assert_eq!(ages(&jane, None, today).as_deref(), Some("60 today"));
        assert_eq!(ages(&Person::default(), released, today), None);
    }

    #[test]
    fn a_birthday_not_yet_reached_does_not_count() {
        assert_eq!(years(date(1966, 10, 2), date(2026, 10, 1)), 59);
        assert_eq!(years(date(1966, 10, 1), date(2026, 10, 1)), 60);
    }
}
