//! The cast row: the credited people in the scene. The library's
//! appearances fact writes a spans file for the item, one span per stretch
//! of the film with the same people in the picture, left to right. The
//! producer cuts a span at nearly every shot change, so a dialogue filmed
//! shot and reverse shot gives one span for each speaker in turn. A row
//! that drew only the span under the playhead would swap its cards on every
//! cut. So the row shows the scene: everyone named in any span that
//! overlaps the last `LINGER` seconds before the playhead.
//!
//! A person who is in the picture now draws at full strength. A person who
//! left the picture draws fainter as the time since their last span grows,
//! and leaves the row when that time reaches `LINGER`.
//!
//! The cards stay in the order the people arrived. A person's arrival is the
//! start of their current run of appearances, where a gap of at most
//! `LINGER` between two spans does not end the run. A newcomer appends at
//! the right, and when a person ages out, the cards to their right move one
//! place left. The order is a function of the spans and the playhead alone,
//! so a seek gives the row that playing to the same second gives.
//!
//! Each card shows the portrait, the actor's name, the character's name in
//! italic and faint the way the library's browser shows it, and a line of
//! ages: how old the person was when the film came out, and how old they are
//! today or the year they died. The file holds dates and not ages, so the
//! row works out each age against the release date and the wall clock.
//!
//! The row is a focus stop above the up-next offer. Up reaches it, and left
//! and right move along it. The focus belongs to a person and not to a
//! place, so a cut that changes the row leaves the focus on the same person.
//! A row with more people than the screen holds runs off the right edge, and
//! the row slides to keep the focused card on screen.

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

/// How long, in seconds of the film, a person stays in the row after their
/// last span ends. It is also the longest gap between two spans of one
/// person that keeps their place in the order.
const LINGER: f64 = 10.0;

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

    /// The people in the scene at one second, in order of arrival, each with
    /// the seconds since their last span ended. A key the file names in a
    /// span but not under `people` draws no card.
    ///
    /// The spans are in time order and do not overlap, so two binary searches
    /// find the spans that overlap the last `LINGER` seconds.
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

    /// Each person's arrival: the start of the earliest span of their current
    /// run, and their place left to right in that span. The walk goes back
    /// from the playhead through the spans before `reach`, and a person's run
    /// ends at the first gap longer than `LINGER`. The walk stops when every
    /// run has ended, so its cost is the number of spans since the earliest
    /// arrival. A person who never leaves the picture for longer than
    /// `LINGER` makes the walk cover every span before the playhead, on
    /// every call.
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
            // Every earlier span ends at or before this span's start, so a
            // run that starts more than `LINGER` after it cannot reach back
            // any further.
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

/// One person's run of appearances as the walk back finds it.
struct Run {
    start: f64,
    place: usize,
    open: bool,
}

/// One card of the row.
#[derive(Debug)]
struct Presence<'a> {
    key: &'a str,
    person: &'a Person,
    /// Seconds since the person's last span ended, and zero while a span
    /// at the playhead names them.
    age: f64,
    arrival: f64,
    place: usize,
}

/// The share of a card's strength at one age: full in the picture, falling
/// in a straight line to nothing at `LINGER`.
fn presence(age: f64) -> f32 {
    (1.0 - age / LINGER).clamp(0.0, 1.0) as f32
}

/// The focus within the row. It names the focused person, so a change to the
/// row leaves the focus on them. The index is where that person last stood,
/// and the focus moves to the card at that index when the person leaves.
#[derive(Debug, Default)]
struct Focus {
    key: Option<String>,
    index: usize,
    /// The first card the row shows.
    first: usize,
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
    /// The focus moves when the row changes under it, and the row changes
    /// with the playhead between two presses, so the draw settles it.
    focus: RefCell<Focus>,
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
        *self.focus.get_mut() = Focus::default();
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

    /// The people in the scene, in the order the row draws them.
    pub fn people(&self, film: &Film) -> Vec<&Person> {
        self.row(film).into_iter().map(|card| card.person).collect()
    }

    fn row(&self, film: &Film) -> Vec<Presence<'_>> {
        match (self.spans.as_ref(), film.position) {
            (Some(spans), Some(second)) => spans.row(second),
            _ => Vec::new(),
        }
    }

    /// The row shows while it holds anyone, in the picture or lingering,
    /// playing or paused, so the display shows who is in the scene whenever
    /// it is up.
    pub fn available(&self, film: &Film) -> bool {
        !self.row(film).is_empty()
    }

    /// Move the focus one card left or right. The focus stops at both ends,
    /// and the row slides only when the focus leaves the cards on screen.
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

    /// The focused card and the first card shown. The focus stays on its
    /// person wherever they now stand. A person who left the row passes the
    /// focus to the card now at their index, or to the last card, so the
    /// focus lands on a neighbour. The first card moves only as far as it
    /// must to keep the focused card on screen, and the row never shows
    /// empty room at its right end while cards wait to the left.
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
            // The card the edge cuts is the hint that the row goes on. It
            // draws only at the screen's own edge, so it never draws under
            // the up-next card.
            if !whole && limit.is_some() {
                break;
            }
            // A lingering card fades as a whole, its box, portrait, and text
            // together, so it reads as the same card on its way out.
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
    fn the_row_holds_the_people_of_the_last_linger_in_order_of_arrival() {
        let cast = cast();
        assert_eq!(names(&cast, &paused_at(12.0)), vec!["John Doe", "Jane Roe"]);
        // John and Jane linger through the gap, and at 30 they are still in
        // the run that arrived at 10, so the two newcomers append to the right.
        assert_eq!(names(&cast, &paused_at(25.0)), vec!["John Doe", "Jane Roe"]);
        assert_eq!(
            names(&cast, &paused_at(31.0)),
            vec!["John Doe", "Jane Roe", "A", "B"]
        );
        assert_eq!(names(&cast, &paused_at(49.0)).len(), 4);
        assert!(names(&cast, &paused_at(50.0)).is_empty());
        assert!(names(&cast, &paused_at(5.0)).is_empty());
    }

    #[test]
    fn the_row_is_a_stop_while_anyone_is_in_it_playing_or_paused() {
        let cast = cast();
        assert!(cast.available(&paused_at(12.0)));
        assert!(cast.available(&paused_at(25.0)));
        assert!(!cast.available(&paused_at(55.0)));
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
        assert_eq!(place(&cast, &film), (3, 2));
        cast.step(1, &film);
        assert_eq!(place(&cast, &film), (3, 2));
        cast.step(-1, &film);
        assert_eq!(place(&cast, &film), (2, 2));
        cast.step(-1, &film);
        assert_eq!(place(&cast, &film), (1, 1));
    }

    /// A row of one-letter people from a list of spans, each as its start,
    /// its end, and its people left to right.
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
        assert!(cast.on_read(item, Some(file)));
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

    /// A dialogue cut shot and reverse shot: A, then B, then A, then B.
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
        assert!(names(&cast, &paused_at(30.0)).is_empty());
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
        // After a gap longer than the linger, b and a arrive again.
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
        // The focus now follows c, and does not return to b's place.
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
