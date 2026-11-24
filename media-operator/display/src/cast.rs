//! The cast row: a card for each credited person in the current scene.
//!
//! The `appearances` tool of `library-operator` writes a spans file for each
//! video. A span is a stretch of the video in which the same people are on
//! screen, listed left to right. The tool starts a new span when the people
//! on screen change, so a dialogue filmed as shot and reverse shot gives one
//! span for each speaker in turn. A row that showed only the span at the
//! playhead would replace its cards on every cut. So the row shows everyone
//! listed in any span that overlaps the last `LINGER` seconds before the
//! playhead.
//!
//! A person who is on screen now is drawn at full strength. A person who has
//! left the picture fades as the time since their last span grows, and leaves
//! the row when that time reaches `LINGER`.
//!
//! Cards are ordered by arrival. A person arrives at the start of their
//! current run of appearances, and a gap of at most `LINGER` between two of
//! their spans does not end the run. A newcomer is added at the right, and
//! when a person leaves, the cards to their right move one place left. The
//! order depends only on the spans and the playhead, so a seek to a second
//! gives the same row as playing to that second.
//!
//! Each card shows the portrait, the actor's name, the character's name in
//! faint italics, and a line of ages: the person's age when the film came
//! out, and their age today or the year they died. The spans file holds
//! dates and not ages, so the row computes the ages from the release date
//! and today's date.
//!
//! The row is a focus stop above the up-next offer. Up moves the focus to
//! it, and left and right move the focus along it. The focus follows a
//! person and not a position, so when a cut changes the row, the focus stays
//! on the same person. A row with more people than fit on the screen runs
//! off the right edge, and the row scrolls to keep the focused card on
//! screen.

use std::cell::{Cell, RefCell};
use std::collections::HashMap;
use std::collections::hash_map::Entry;

use iced::{Point, Rectangle, Size};
use jiff::civil::Date;
use serde_json::Value;

use crate::art::Art;
use crate::canvas::{Anchor, Brush, Canvas, Line, clip};
use crate::film::Film;
use crate::header::BETWEEN;
use crate::theme;
use crate::upnext::{CARD_ALPHA, CARD_R};

/// The size of the portrait, which the decoded image fills.
pub const PORTRAIT_W: f32 = 57.0;
pub const PORTRAIT_H: f32 = 76.0;

/// The card's text sizes. The row shows while the film plays, so its text is
/// smaller than the control bar's, and the cards cover less of the picture.
const NAME_SIZE: f32 = 30.0;
const LINE_SIZE: f32 = 24.0;

/// The size of one card and the space between two cards. The card is wide
/// enough for a long name and for the ages line at these text sizes.
const CARD_W: f32 = 330.0;
const CARD_H: f32 = PORTRAIT_H + 2.0 * PAD;
const GAP: f32 = 14.0;
const PAD: f32 = 8.0;
const TEXT_GAP: f32 = 12.0;

/// The top edge of the cards and of the heading. The cards end above the
/// skip control and the up-next chip, so the row stays in place when either
/// one appears.
const ROW_TOP: f32 = 712.0;
const HEADING_Y: f32 = 676.0;
/// The highest y that the row draws at, the top of the heading.
pub const TOP: f32 = HEADING_Y;
const HEADING: &str = "In this scene";

/// The offset from the card's top edge to the top of each line of text.
const NAME_Y: f32 = 5.0;
const CHARACTER_Y: f32 = 34.0;
const AGES_Y: f32 = 58.0;

/// While the up-next card is showing, the row ends this far to the left of
/// the card.
pub const CARD_CLEAR: f32 = 24.0;

/// The card cut off by the right edge is drawn at this fraction of normal
/// strength, to show that more people are further right.
const PEEK: f32 = 0.35;

/// How long, in seconds of film time, a person stays in the row after their
/// last span ends. It is also the longest gap between two spans of one
/// person that still counts as the same run of appearances.
const LINGER: f64 = 10.0;

/// The `format` value of the spans file that the `appearances` tool writes.
const FORMAT: &str = "liken.sh/appearances/spans/v1";

/// The data a card shows for one person.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Person {
    pub name: String,
    pub character: Option<String>,
    /// The portrait's path relative to the library's `.contributors`
    /// directory.
    pub portrait: Option<String>,
    pub born: Option<Date>,
    pub died: Option<Date>,
}

/// One span: its start and end in seconds, and its people, left to right.
#[derive(Debug, Clone, PartialEq)]
struct Span {
    start: f64,
    end: f64,
    people: Vec<String>,
}

/// The parsed spans file of one item.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Spans {
    released: Option<Date>,
    people: HashMap<String, Person>,
    spans: Vec<Span>,
}

impl Spans {
    /// Parses the text of a spans file. Returns `None` for another format or
    /// for text that does not parse, and the item then shows no row.
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

    /// Reads and parses the spans file at `path`. Call this on the blocking
    /// pool, because the file is on the library's volume and a slow network
    /// read must not hold up a frame.
    pub fn read(path: &str) -> Option<Self> {
        let text = std::fs::read_to_string(path).ok();
        if text.is_none() {
            eprintln!("media-display: cannot read the spans file {path}");
        }
        Self::parse(&text?)
    }

    /// The people in the scene at `second`, in order of arrival, each with
    /// the seconds since their last span ended. A key that a span lists but
    /// that is missing from `people` gets no card.
    ///
    /// The spans are sorted by time and do not overlap, so two binary
    /// searches find the spans that overlap the last `LINGER` seconds.
    fn row(&self, second: f64) -> Vec<Presence<'_>> {
        let first = self
            .spans
            .partition_point(|span| span.end <= second - LINGER);
        let reach = self.spans.partition_point(|span| span.start <= second);
        let mut ages: HashMap<&str, f64> = HashMap::new();
        for span in &self.spans[first.min(reach)..reach] {
            let age = (second - span.end).max(0.0);
            for key in &span.people {
                if self.people.contains_key(key) {
                    ages.insert(key, age);
                }
            }
        }
        let arrivals = self.arrivals(&ages, reach);
        let mut row: Vec<Presence<'_>> = ages
            .into_iter()
            .map(|(key, age)| {
                let (arrival, place) = arrivals[key];
                Presence {
                    key,
                    person: &self.people[key],
                    age,
                    arrival,
                    place,
                }
            })
            .collect();
        row.sort_by(|a, b| {
            a.arrival
                .total_cmp(&b.arrival)
                .then(a.place.cmp(&b.place))
                .then(a.key.cmp(b.key))
        });
        row
    }

    /// Each person's arrival: the start of the earliest span of their
    /// current run, and their position from the left in that span. It walks
    /// backwards from `reach`, ends a person's run at the first gap longer
    /// than `LINGER`, and stops when every run has ended. When a person is
    /// never absent for longer than `LINGER`, each call walks every span
    /// before the playhead.
    fn arrivals<'a>(
        &self,
        people: &HashMap<&'a str, f64>,
        reach: usize,
    ) -> HashMap<&'a str, (f64, usize)> {
        let mut runs: HashMap<&'a str, Run> = HashMap::new();
        let mut open = people.len();
        for span in self.spans[..reach].iter().rev() {
            if open == 0 {
                break;
            }
            for (place, key) in span.people.iter().enumerate() {
                let Some((key, _)) = people.get_key_value(key.as_str()) else {
                    continue;
                };
                match runs.entry(key) {
                    Entry::Vacant(entry) => {
                        entry.insert(Run {
                            start: span.start,
                            place,
                            open: true,
                        });
                    }
                    Entry::Occupied(mut entry) => {
                        let run = entry.get_mut();
                        if run.open && run.start - span.end <= LINGER {
                            run.start = span.start;
                            run.place = place;
                        }
                    }
                }
            }
            // Every earlier span ends at or before this span's start. A run
            // that starts more than `LINGER` after this span's start has a
            // gap longer than `LINGER` before it, so no earlier span can
            // extend it.
            for run in runs.values_mut().filter(|run| run.open) {
                if run.start - span.start > LINGER {
                    run.open = false;
                    open -= 1;
                }
            }
        }
        runs.into_iter()
            .map(|(key, run)| (key, (run.start, run.place)))
            .collect()
    }
}

/// One person's run of appearances, as `arrivals` builds it while it walks
/// backwards.
struct Run {
    start: f64,
    place: usize,
    open: bool,
}

/// One person in the row, before the row is drawn.
#[derive(Debug)]
struct Presence<'a> {
    key: &'a str,
    person: &'a Person,
    /// Seconds since the person's last span ended, or zero while a span at
    /// the playhead lists them.
    age: f64,
    arrival: f64,
    place: usize,
}

/// The strength of a card for a given age: 1.0 while the person is on
/// screen, falling linearly to 0.0 at `LINGER`.
fn presence(age: f64) -> f32 {
    (1.0 - age / LINGER).clamp(0.0, 1.0) as f32
}

/// The focus within the row. It records the focused person by key, so a
/// change to the row keeps the focus on that person. `index` is the person's
/// last known position, and when the person leaves the row, the focus moves
/// to the card now at that index.
#[derive(Debug, Default)]
struct Focus {
    key: Option<String>,
    index: usize,
    /// The index of the first card that is drawn.
    first: usize,
}

/// The state of the row: the item's spans, the library's `.contributors`
/// directory, and the focus.
#[derive(Debug, Default)]
pub struct Cast {
    /// A counter that `on_item` increments for each new item. A read result
    /// carries the value it was started with, so the result of a read for a
    /// previous item is discarded.
    item: u64,
    spans: Option<Spans>,
    contributors: Option<String>,
    /// The row changes with the playhead between key presses, and the focus
    /// must follow, so `draw` updates it through `&self`.
    focus: RefCell<Focus>,
    /// How many whole cards fit in the last frame. A key press reads it to
    /// decide when the row has to scroll.
    room: Cell<usize>,
}

impl Cast {
    /// Starts a new item and discards the previous item's spans. Returns
    /// the item counter and the path of the spans file to read, or `None`
    /// when the item has no spans file. Pass the counter back to `on_read`.
    pub fn on_item(
        &mut self,
        appearances: Option<&str>,
        contributors: Option<&str>,
    ) -> Option<(u64, String)> {
        self.item += 1;
        self.spans = None;
        self.contributors = contributors.map(str::to_string);
        *self.focus.get_mut() = Focus::default();
        Some((self.item, appearances?.to_string()))
    }

    /// Stores the result of a spans file read. Returns `false` and discards
    /// it when `item` is not the current item.
    pub fn on_read(&mut self, item: u64, spans: Option<Spans>) -> bool {
        if item != self.item {
            return false;
        }
        self.spans = spans;
        true
    }

    /// The people in the row, in the order the row draws them.
    pub fn people(&self, film: &Film) -> Vec<&Person> {
        self.row(film).into_iter().map(|card| card.person).collect()
    }

    fn row(&self, film: &Film) -> Vec<Presence<'_>> {
        match (self.spans.as_ref(), film.position) {
            (Some(spans), Some(second)) => spans.row(second),
            _ => Vec::new(),
        }
    }

    /// Whether the row has anyone to show, including people who left the
    /// picture less than `LINGER` seconds ago. It does not depend on whether
    /// the film plays or is paused.
    pub fn available(&self, film: &Film) -> bool {
        !self.row(film).is_empty()
    }

    /// Moves the focus one card left or right. The focus stops at both ends
    /// and does not wrap. The row scrolls only when the focus would leave the
    /// cards on screen.
    pub fn step(&mut self, direction: i64, film: &Film) {
        let row = self.row(film);
        if row.is_empty() {
            return;
        }
        let (focus, _) = self.place(&row);
        let index = (focus as i64 + direction).clamp(0, row.len() as i64 - 1) as usize;
        {
            let mut state = self.focus.borrow_mut();
            state.key = Some(row[index].key.to_string());
            state.index = index;
        }
        self.place(&row);
    }

    /// Returns the index of the focused card and the index of the first
    /// card drawn, and saves both. When the focused person has left the row,
    /// the focus moves to the card now at their old index, or to the last
    /// card. The first card moves only as far as needed to keep the focused
    /// card on screen, and the row leaves no empty space at its right end
    /// while cards are scrolled off the left.
    fn place(&self, row: &[Presence<'_>]) -> (usize, usize) {
        let mut state = self.focus.borrow_mut();
        let Some(last) = row.len().checked_sub(1) else {
            return (0, 0);
        };
        let index = state
            .key
            .as_deref()
            .and_then(|key| row.iter().position(|card| card.key == key))
            .unwrap_or(state.index.min(last));
        let room = self.room.get().max(1);
        let first = state
            .first
            .clamp((index + 1).saturating_sub(room), index)
            .min(row.len().saturating_sub(room));
        *state = Focus {
            key: Some(row[index].key.to_string()),
            index,
            first,
        };
        (index, first)
    }

    /// The paths of the portraits that the row draws now.
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

    /// Draws the heading and the cards. `limit` is the x where the row must
    /// end: the left edge of the up-next card minus `CARD_CLEAR` while that
    /// card is showing, and `None` otherwise, so the cards run off the right
    /// edge of the screen.
    pub fn draw(
        &self,
        brush: &mut Brush<'_>,
        film: &Film,
        art: &Art,
        focused: bool,
        limit: Option<f32>,
        today: Date,
    ) {
        let row = self.row(film);
        if row.is_empty() {
            return;
        }
        let canvas = brush.canvas();
        let end = limit.unwrap_or(canvas.width());
        let room = (((end - theme::MARGIN_X + GAP) / (CARD_W + GAP)).floor() as usize).max(1);
        self.room.set(room);
        let (focus, first) = self.place(&row);
        let released = self.spans.as_ref().and_then(|spans| spans.released);

        brush.text(Line::new(
            HEADING,
            Point::new(theme::MARGIN_X, HEADING_Y),
            Anchor::TopLeft,
            theme::type_scale::TINY,
            theme::color::muted(),
        ));
        for (place, card) in row.iter().enumerate().skip(first) {
            let x = theme::MARGIN_X + (place - first) as f32 * (CARD_W + GAP);
            if x >= end {
                break;
            }
            let bounds = Rectangle::new(Point::new(x, ROW_TOP), Size::new(CARD_W, CARD_H));
            let whole = x + CARD_W <= end;
            // A card cut off by the screen edge hints that the row goes on.
            // It is drawn only at the screen's own edge, so a card is never
            // drawn partly under the up-next card.
            if !whole && limit.is_some() {
                break;
            }
            // A lingering card fades as a unit, with its box, portrait, and
            // text together, so it still reads as the same card.
            let fade = brush.fade() * presence(card.age);
            let fade = match whole {
                true => fade,
                false => fade * PEEK,
            };
            let portrait = self
                .portrait_path(card.person)
                .and_then(|path| art.portrait(&path));
            brush.at_fade(fade, |brush| {
                draw_card(
                    brush,
                    &canvas,
                    bounds,
                    card.person,
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
/// death. A part with no date to compute it from is left out, and a person
/// with no dates has no line.
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

/// Whole years from `from` to the later date `to`. A birthday that has not
/// yet come in `to`'s year is not counted.
fn years(from: Date, to: Date) -> i16 {
    let before = (to.month(), to.day()) < (from.month(), from.day());
    to.year() - from.year() - i16::from(before)
}

#[cfg(test)]
mod tests {
    use super::*;
    use jiff::civil::date;
    use serde_json::json;

    /// The spans file of a film with two spans and a gap between them: John
    /// and Jane together, then four people in one shot.
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
        assert!(
            cast.on_read(item, Spans::parse(&file())),
            "the read for the current item was discarded"
        );
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
    fn the_row_shows_the_people_of_the_last_linger_in_order_of_arrival() {
        let cast = cast();
        assert_eq!(names(&cast, &paused_at(12.0)), vec!["John Doe", "Jane Roe"]);
        // John and Jane linger through the gap, and at 30 they are still in
        // the run that arrived at 10, so the two newcomers are added at the
        // right.
        assert_eq!(names(&cast, &paused_at(25.0)), vec!["John Doe", "Jane Roe"]);
        assert_eq!(
            names(&cast, &paused_at(31.0)),
            vec!["John Doe", "Jane Roe", "A", "B"]
        );
        assert_eq!(names(&cast, &paused_at(49.0)).len(), 4);
        assert!(
            names(&cast, &paused_at(50.0)).is_empty(),
            "the row is empty after the last span ends"
        );
        assert!(
            names(&cast, &paused_at(5.0)).is_empty(),
            "the row is empty before the first span starts"
        );
    }

    #[test]
    fn the_row_is_a_stop_while_anyone_is_in_it_playing_or_paused() {
        let cast = cast();
        assert!(
            cast.available(&paused_at(12.0)),
            "no stop at 12 seconds while paused"
        );
        assert!(
            cast.available(&paused_at(25.0)),
            "no stop at 25 seconds while paused"
        );
        assert!(
            !cast.available(&paused_at(55.0)),
            "a stop at 55 seconds, when nobody is in the row"
        );
        let playing = Film {
            paused: false,
            ..paused_at(12.0)
        };
        assert!(
            cast.available(&playing),
            "no stop at 12 seconds while playing"
        );
        assert!(
            !Cast::default().available(&paused_at(12.0)),
            "a stop with no spans loaded"
        );
    }

    #[test]
    fn a_read_that_finishes_after_the_item_changed_is_discarded() {
        let mut cast = Cast::default();
        let (stale, _) = cast.on_item(Some("a"), None).unwrap();
        cast.on_item(Some("b"), None);
        assert!(
            !cast.on_read(stale, Spans::parse(&file())),
            "a read for a previous item was accepted"
        );
        assert!(
            names(&cast, &paused_at(12.0)).is_empty(),
            "the discarded read filled the row"
        );
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
    fn the_focus_moves_along_the_row_and_the_row_scrolls_to_keep_it_on_screen() {
        let mut cast = cast();
        let film = paused_at(35.0);
        cast.room.set(2);
        for _ in 0..3 {
            cast.step(1, &film);
        }
        assert_eq!(place(&cast, &film), (3, 2));
        cast.step(1, &film);
        assert_eq!(place(&cast, &film), (3, 2));
        cast.step(-1, &film);
        assert_eq!(place(&cast, &film), (2, 2));
        cast.step(-1, &film);
        assert_eq!(place(&cast, &film), (1, 1));
    }

    /// A cast of one-letter people built from a list of spans, each given as
    /// its start, its end, and its people from left to right.
    fn scene(spans: &[(f64, f64, &[&str])]) -> Cast {
        let people = spans
            .iter()
            .flat_map(|(_, _, people)| people.iter())
            .map(|key| {
                let person = Person {
                    name: key.to_string(),
                    ..Person::default()
                };
                (key.to_string(), person)
            })
            .collect();
        let spans = spans
            .iter()
            .map(|(start, end, people)| Span {
                start: *start,
                end: *end,
                people: people.iter().map(|key| key.to_string()).collect(),
            })
            .collect();
        let mut cast = Cast::default();
        let (item, _) = cast.on_item(Some("spans.json"), None).unwrap();
        let file = Spans {
            released: None,
            people,
            spans,
        };
        assert!(
            cast.on_read(item, Some(file)),
            "the read for the current item was discarded"
        );
        cast
    }

    fn place(cast: &Cast, film: &Film) -> (usize, usize) {
        cast.place(&cast.row(film))
    }

    fn focused(cast: &Cast, film: &Film) -> String {
        let row = cast.row(film);
        let (focus, _) = cast.place(&row);
        row[focus].person.name.clone()
    }

    /// A dialogue cut as shot and reverse shot: A, then B, then A, then B.
    fn dialogue() -> Cast {
        scene(&[
            (0.0, 4.0, &["a"]),
            (4.0, 8.0, &["b"]),
            (8.0, 12.0, &["a"]),
            (12.0, 16.0, &["b"]),
            (16.0, 20.0, &["a"]),
        ])
    }

    #[test]
    fn a_shot_and_reverse_shot_keep_both_people_in_one_order() {
        let cast = dialogue();
        let cases = [
            (5.0, ["a", "b"]),
            (9.0, ["a", "b"]),
            (13.0, ["a", "b"]),
            (17.0, ["a", "b"]),
        ];
        for (second, row) in cases {
            assert_eq!(names(&cast, &paused_at(second)), row, "at {second}");
        }
        assert_eq!(names(&cast, &paused_at(2.0)), vec!["a"]);
    }

    #[test]
    fn a_person_ages_out_one_linger_after_their_last_span() {
        let cast = dialogue();
        assert_eq!(names(&cast, &paused_at(25.9)), vec!["a", "b"]);
        assert_eq!(names(&cast, &paused_at(26.0)), vec!["a"]);
        assert!(
            names(&cast, &paused_at(30.0)).is_empty(),
            "the row is not empty at 30 seconds"
        );
    }

    #[test]
    fn a_lingering_card_fades_with_its_age() {
        let cases = [(0.0, 1.0), (LINGER / 2.0, 0.5), (LINGER, 0.0)];
        for (age, alpha) in cases {
            assert_eq!(presence(age), alpha, "at age {age}");
        }
        let cast = dialogue();
        let row = cast.row(&paused_at(21.0));
        let ages: Vec<f64> = row.iter().map(|card| card.age).collect();
        assert_eq!(ages, vec![1.0, 5.0]);
    }

    #[test]
    fn a_newcomer_appends_to_the_right_and_ties_keep_the_picture_order() {
        let cast = scene(&[
            (0.0, 5.0, &["c", "b"]),
            (5.0, 10.0, &["a"]),
            (10.0, 15.0, &["b", "a", "c"]),
        ]);
        assert_eq!(names(&cast, &paused_at(12.0)), vec!["c", "b", "a"]);
        // After a gap longer than `LINGER`, b and a arrive again.
        let cast = scene(&[(0.0, 5.0, &["a", "b"]), (20.0, 25.0, &["b", "a"])]);
        assert_eq!(names(&cast, &paused_at(21.0)), vec!["b", "a"]);
    }

    #[test]
    fn a_seek_gives_the_row_that_playing_to_the_same_second_gives() {
        let mut played = dialogue();
        for second in [1.0, 5.0, 9.0, 13.0] {
            played.step(1, &paused_at(second));
        }
        let sought = dialogue();
        assert_eq!(
            names(&played, &paused_at(17.0)),
            names(&sought, &paused_at(17.0))
        );
    }

    #[test]
    fn the_focus_follows_its_person_across_a_cut() {
        let mut cast = dialogue();
        cast.step(1, &paused_at(5.0));
        assert_eq!(focused(&cast, &paused_at(5.0)), "b");
        assert_eq!(focused(&cast, &paused_at(9.0)), "b");
        assert_eq!(focused(&cast, &paused_at(13.0)), "b");
    }

    #[test]
    fn the_focus_stays_on_its_person_when_someone_to_the_left_ages_out() {
        let mut cast = scene(&[(0.0, 4.0, &["a", "b", "c"]), (4.0, 30.0, &["b", "c"])]);
        cast.step(1, &paused_at(2.0));
        assert_eq!(focused(&cast, &paused_at(2.0)), "b");
        assert_eq!(names(&cast, &paused_at(20.0)), vec!["b", "c"]);
        assert_eq!(focused(&cast, &paused_at(20.0)), "b");
        assert_eq!(place(&cast, &paused_at(20.0)), (0, 0));
    }

    #[test]
    fn the_focus_moves_to_the_neighbour_when_its_person_ages_out() {
        let mut cast = scene(&[(0.0, 4.0, &["a", "b", "c"]), (4.0, 30.0, &["a", "c"])]);
        cast.step(1, &paused_at(2.0));
        assert_eq!(focused(&cast, &paused_at(2.0)), "b");
        assert_eq!(focused(&cast, &paused_at(20.0)), "c");
        // The focus now follows c and does not go back to b's position.
        assert_eq!(focused(&cast, &paused_at(25.0)), "c");
    }

    #[test]
    fn the_focus_clamps_to_the_last_card_when_the_end_of_the_row_ages_out() {
        let mut cast = scene(&[(0.0, 4.0, &["a", "b"]), (4.0, 30.0, &["a"])]);
        cast.step(1, &paused_at(2.0));
        assert_eq!(focused(&cast, &paused_at(20.0)), "a");
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
