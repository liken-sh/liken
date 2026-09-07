// The franchise page's wall, measured before anything draws. The wall is
// one lane of rows in story order, one row per entry, first to last; two
// entries the story tells at once each take a row of their own. An era
// is a heading over the first row it covers, and the heading holds at
// the top of the lane while the era's rows scroll under it. The rows
// follow the order and never the times, because a franchise runs from
// 1260 BC to 2028 with most of it in twenty years, and a time scale is
// then one dot and an empty lane. The universes are the lines of the
// metro strip beside the lane: the franchise's own first, then every
// other universe an entry names, in first-seen order, and the strip packs
// their runs into lanes. A held entry is a card as tall as its art and
// the gaps around it, and an entry no library holds is a thin row, so the
// rows are not one height, and every measure of the wall reads the row
// tops the wall lays out once.

use iced_winit::core::Rectangle;

use super::metro;
use crate::catalog::franchise::{Entry, Era, Franchise, Held, SERIES, SPAN, Standing};
use crate::catalog::{Calendar, art};
use crate::look;
use crate::screens::facts;
use crate::views::{REACH, area, text, wall};

/// The space under a row, inside a card, and between the strip and the
/// cards.
pub const GAP: f32 = 16.0;

/// The narrowest the time column is: the room a four-digit year takes,
/// and the gap between the column and the strip. A franchise numbered in
/// one or two digits keeps that floor, so its cards start about where the
/// cards of a franchise numbered in years do.
pub fn floor() -> f32 {
    text::measured(YEAR, look::CAPTION) + GAP
}

// The widest number a plain calendar writes, which sets the floor.
const YEAR: &str = "2026";

/// The width the time column takes for these rows: the widest label the
/// wall draws, on the wider of the two lines it stacks a span on, and the
/// column's own gap, never under the floor. A wall no row labels takes no
/// column at all.
pub fn time_width(rows: &[Row]) -> f32 {
    if !labelled(rows) {
        return 0.0;
    }
    let widest = rows.iter().fold(0.0_f32, |wide, row| {
        let (first, second) = stacked(&row.time);
        wide.max(text::measured(&first, look::CAPTION))
            .max(text::measured(&second, look::CAPTION))
    });
    (widest + GAP).max(floor())
}

/// One time label as the column draws it: the first time on one line, and
/// "to" with the second time on the next. A span stacked this way never
/// widens the column past one time and its mark, and the column is as
/// narrow as the times it holds. A label of one time takes the first line
/// alone.
pub fn stacked(time: &str) -> (String, String) {
    match time.split_once(SPAN) {
        Some((first, second)) => (first.to_string(), format!("to {second}")),
        None => (time.to_string(), String::new()),
    }
}

/// The time label one row draws: nothing where the row above it carries
/// the same label. A run of rows in one year prints the year once, on the
/// row where the year starts, so the column reads as the times the story
/// passes through and not as one number repeated.
pub fn label_at(rows: &[Row], row: usize) -> &str {
    let Some(here) = rows.get(row) else {
        return "";
    };
    let above = row.checked_sub(1).and_then(|prior| rows.get(prior));
    match above.is_some_and(|above| above.time == here.time) {
        true => "",
        false => &here.time,
    }
}

/// The caption the band writes under the page's title: "Years from the
/// Battle of Yavin". The times in the column count from one event, and
/// the caption is where the page says which event and in what unit. It
/// is one line in the band and not a stack over the column, because the
/// column is as wide as one time and its mark, and a caption wrapped
/// into that width pushed the first row down a screen's worth. A
/// calendar with no zero carries none, and so does a wall with no
/// column, because then there are no times to caption.
pub fn caption(calendar: &Option<Calendar>, time: f32) -> String {
    let Some(calendar) = calendar else {
        return String::new();
    };
    match time > 0.0 {
        true => calendar.caption(),
        false => String::new(),
    }
}

/// The space over the first row: the room the mark of a focused first
/// row reaches into, and no more.
pub const HEAD: f32 = REACH;

/// The space under the last row.
pub const TAIL: f32 = 36.0;

/// The height of the thin row an entry no library holds draws as.
pub const THIN: f32 = 56.0;

/// The height of every card's art: a share of the room the rows have.
/// The width follows the height at 16:9. The share puts a little under
/// three cards on a screen, so the art is large and the wall still reads
/// as a list.
pub fn art_height(rows: f32) -> f32 {
    (rows * CAP).max(0.0)
}

/// The share of the rows' room one card's art takes.
const CAP: f32 = 0.3;

/// The width of the poster a cell falls back to, at the wall's own poster
/// ratio and the art's own height.
pub fn poster_width(art: f32) -> f32 {
    art / wall::POSTER
}

/// The height one card takes: the art and a gap over and under it.
pub fn card_height(art: f32) -> f32 {
    art + 2.0 * GAP
}

/// One entry's title as the two lines beside its art, at the name size.
/// The first line breaks on a word where the width holds one, and the
/// second ends in an ellipsis where the title runs past it.
pub fn titled(name: &str, width: f32) -> (String, String) {
    let room = text::fits(look::NAME, width).max(1);
    if name.chars().count() <= room {
        return (name.to_string(), String::new());
    }
    let at = name
        .char_indices()
        .nth(room)
        .map_or(name.len(), |(index, _)| index);
    let broken = name[..at]
        .rfind(' ')
        .map_or(room, |space| name[..space].chars().count());
    let first: String = name.chars().take(broken).collect();
    let rest: String = name.chars().skip(broken).collect();
    (
        first.trim_end().to_string(),
        text::cut(rest.trim_start(), look::NAME, width),
    )
}

/// One entry as the wall draws it. `universes` is the index of every
/// universe the entry names, in the order the file names them, and the
/// franchise's own where it names none; the strip takes a dot on each of
/// their lines. `library`, `kind`, and `id` are the item a press opens,
/// and all three are empty for a gap, which opens nothing. `wide` says
/// whether the art fills the card's 16:9 box: the landscape art of a
/// title fills it, and the poster a card falls back to draws at its own
/// ratio at the left of it. `year` is the release year the card or the
/// thin row draws beside the title, and `blurb` is the item's tagline,
/// or its plot where it has no tagline, empty for a gap.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Cell {
    pub universes: Vec<usize>,
    pub library: String,
    pub kind: String,
    pub id: String,
    pub art: String,
    pub wide: bool,
    pub name: String,
    /// The facts line beside the title: the kind, the year, and a film's
    /// running time or a series run's episodes, dots between, the way the
    /// film's page words them.
    pub facts: String,
    pub blurb: String,
    /// Whether the blurb is the item's tagline, which draws in the
    /// italic, and not the first lines of its plot.
    pub tagline: bool,
    pub note: String,
    pub standing: Standing,
}

impl Cell {
    /// The library, the kind, and the id a press opens, and nothing for a gap.
    pub fn opens(&self) -> Option<(&str, &str, &str)> {
        match self.id.is_empty() {
            true => None,
            false => Some((&self.library, &self.kind, &self.id)),
        }
    }

    /// Whether some library holds the entry. A held entry is a card, and
    /// any other is a thin row.
    pub fn held(&self) -> bool {
        self.standing == Standing::Held
    }
}

/// One row of the wall: its one cell, and the span the row covers on the
/// franchise's clock. `time` is the label the row draws, empty where the
/// entry carries no time or the file names no calendar.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Row {
    pub cell: Cell,
    pub time: String,
    pub timed: bool,
    pub from: f64,
    pub to: f64,
}

impl Row {
    /// The height this row takes: a card for a held entry, and the thin
    /// row for a gap.
    pub fn height(&self, art: f32) -> f32 {
        match self.cell.held() {
            true => card_height(art),
            false => THIN,
        }
    }
}

/// The universes, as the lines of the strip. A run and a cell both name
/// one by its place in this list. The franchise's own is the first, even
/// where the file names none, because an entry with no universes is in
/// it. Every other universe an entry names follows, in first-seen order.
pub fn columns(franchise: &Franchise) -> Vec<String> {
    let mut named = vec![franchise.universe.clone()];
    for entry in &franchise.entries {
        for universe in &entry.universes {
            if !named.contains(universe) {
                named.push(universe.clone());
            }
        }
    }
    named
}

/// The wall in story order: one row per entry, first to last. `today` is
/// the ISO date the standing of a gap reads.
pub fn story(franchise: &Franchise, columns: &[String], today: &str) -> Vec<Row> {
    franchise
        .entries
        .iter()
        .map(|entry| row(entry, &franchise.calendar, cell(entry, columns, today)))
        .collect()
}

/// One era as a heading over the rows it covers: its name, how long it
/// runs, the first and the last row it covers, and how deep it nests.
/// The file writes no row, so the headings are derived.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Heading {
    pub name: String,
    /// How long the era runs in the calendar's own unit, "3441 years",
    /// and nothing where the file names no calendar.
    pub count: String,
    pub first: usize,
    pub last: usize,
    /// How many wider eras start on the same row. An era of depth zero
    /// draws as a heading with a rule, and one deeper as a sub-heading
    /// under it, so a phase reads as part of its saga.
    pub depth: usize,
}

impl Heading {
    /// The words the heading reads as, "The Second Age · 3441 years".
    pub fn label(&self) -> String {
        match self.count.is_empty() {
            true => self.name.clone(),
            false => format!("{}{SPAN_MARK}{}", self.name, self.count),
        }
    }
}

/// The run of a heading's words that reads bright: everything before the
/// count, so "The Infinity Saga › Phase Two" of "The Infinity Saga › Phase
/// Two · 3 years", and the whole of words with no count.
pub fn bright(words: &str) -> &str {
    match words.find(SPAN_MARK) {
        Some(at) => &words[..at],
        None => words,
    }
}

/// How long an era runs, in the calendar's own unit: "3441 years". The
/// count is inclusive of both ends, the way a person says "from 1 to
/// 3441"; an era of one year says "1 year". A file with no calendar
/// counts nothing.
pub fn counted(era: &Era, calendar: &Option<Calendar>) -> String {
    let Some(calendar) = calendar else {
        return String::new();
    };
    let count = (era.to - era.from).abs().round() as i64 + 1;
    let unit = match count {
        1 => calendar.unit.trim_end_matches('s').to_string(),
        _ => calendar.unit.clone(),
    };
    format!("{count} {unit}")
}

/// The mark between a heading's name and its count.
pub const SPAN_MARK: &str = " · ";

/// The mark between the eras of the held line, outer to inner.
pub const CRUMB_MARK: &str = " › ";

/// The height one heading takes over its first row.
pub const HEADING: f32 = 52.0;

/// The half pixel inside which two edges count as one, so the scroll's
/// rounding never decides whether the held line shows.
pub const SLACK: f32 = 0.5;

/// The eras as headings, in the order the wall draws them: by the first
/// row each covers, and the widest first where several start on one
/// row, so an Age reads over the stretch inside it. A heading marks
/// where the story enters an era: the first timed row whose span starts
/// inside the era's, and not the first row the era merely touches,
/// because a long row that a later era overlaps would otherwise wear
/// that era's heading too. From there the era covers one unbroken run
/// of rows, to the last before a timed row its span does not meet. The
/// wall is in story order and not in time order, and a story jumps
/// back, so a row far down the wall may meet an era that ended long
/// before it; a heading that reached that row would hold over everything
/// between. A row with no time sits inside whatever era surrounds it
/// and breaks no run. An era no row starts inside draws no heading.
pub fn headings(eras: &[Era], rows: &[Row], calendar: &Option<Calendar>) -> Vec<Heading> {
    let mut headings: Vec<(f64, Heading)> = eras
        .iter()
        .filter_map(|era| {
            let meets = |row: &Row| row.timed && era.meets(row.from, row.to);
            let first = rows
                .iter()
                .position(|row| row.timed && era.from <= row.from && row.from <= era.to)?;
            let last = rows
                .iter()
                .enumerate()
                .skip(first)
                .take_while(|(_, row)| !row.timed || meets(row))
                .filter(|(_, row)| row.timed)
                .map(|(index, _)| index)
                .last()
                .unwrap_or(first);
            Some((
                era.width(),
                Heading {
                    name: era.name.clone(),
                    count: counted(era, calendar),
                    first,
                    last,
                    depth: 0,
                },
            ))
        })
        .collect();
    headings.sort_by(|(one_width, one), (other_width, other)| {
        one.first.cmp(&other.first).then_with(|| {
            other_width
                .partial_cmp(one_width)
                .unwrap_or(std::cmp::Ordering::Equal)
        })
    });
    let mut headings: Vec<Heading> = headings.into_iter().map(|(_, heading)| heading).collect();
    for index in 0..headings.len() {
        headings[index].depth = headings[..index]
            .iter()
            .filter(|other| other.first == headings[index].first)
            .count();
    }
    headings
}

/// How many headings stand over this row: every era that starts on it.
pub fn over(headings: &[Heading], row: usize) -> usize {
    headings
        .iter()
        .filter(|heading| heading.first == row)
        .count()
}

/// How many headings stand between the top of the lane and this row
/// when the row is scrolled to the top: its own inline ones, and the
/// held line where the row is inside an era that started above it, so
/// the held line never covers the row.
pub fn reach(headings: &[Heading], row: usize) -> usize {
    let inside = headings
        .iter()
        .any(|heading| heading.first < row && row <= heading.last);
    over(headings, row) + usize::from(inside)
}

/// The first row of the era before the one this row is in: the nearest
/// heading that starts above the row. Left steps here.
pub fn before(headings: &[Heading], row: usize) -> Option<usize> {
    headings
        .iter()
        .map(|heading| heading.first)
        .filter(|first| *first < row)
        .max()
}

/// The first row of the next era: the nearest heading that starts under
/// the row. Right steps here.
pub fn after(headings: &[Heading], row: usize) -> Option<usize> {
    headings
        .iter()
        .map(|heading| heading.first)
        .filter(|first| *first > row)
        .min()
}

// One entry as a cell. An entry that names several universes takes a dot
// on each of their lines, and the strip draws a bar across the dots.
fn cell(entry: &Entry, columns: &[String], today: &str) -> Cell {
    let mut universes: Vec<usize> = entry
        .universes
        .iter()
        .filter_map(|universe| columns.iter().position(|column| column == universe))
        .collect();
    if universes.is_empty() {
        universes.push(0);
    }
    let held = entry.held.clone().unwrap_or_default();
    let (art, wide) = drawn(&held);
    let facts = facts(entry, &held);
    Cell {
        universes,
        library: held.library,
        kind: held.kind,
        id: held.id,
        art,
        wide,
        name: entry.name().to_string(),
        facts,
        tagline: !held.tagline.is_empty(),
        blurb: match held.tagline.is_empty() {
            true => held.plot,
            false => held.tagline,
        },
        note: note(entry, today),
        standing: entry.standing(today),
    }
}

// The year a card or a thin row draws beside its title: the file's
// release year, or the year of the held item's own release date where
// the file gives none.
fn dated(entry: &Entry, held: &Held) -> String {
    match entry.release_year > 0 {
        true => entry.release_year.to_string(),
        false => held.released.chars().take(4).collect(),
    }
}

/// The facts line: Film or Series, the year, and then a film's running
/// time or a series run's episodes where the catalog holds them. A fact
/// the entry does not carry leaves no gap and no dot behind.
/// The facts under one entry: the kind word, the year, and a film's
/// running time or a series run's episodes. The franchise strip on a
/// film's page and on a series' page draws the same line under its
/// members, so the words are spelled once.
pub fn facts(entry: &Entry, held: &Held) -> String {
    let series = entry.kind == SERIES;
    let held_facts = match (entry.held.is_some(), series) {
        (true, true) => facts::counted(entry.episodes, "episodes"),
        (true, false) => facts::runtime(held.duration),
        (false, _) => String::new(),
    };
    facts::joined(&[
        facts::kind_word(&entry.kind),
        &dated(entry, held),
        &held_facts,
    ])
}

// The art one cell draws, and whether it fills the cell's 16:9 box: the
// item's own 16:9 art, and its poster where it holds none. A poster does
// not fill a landscape box, so the cell says so and the page draws it at
// its own ratio. A gap holds no item and names no art.
fn drawn(held: &Held) -> (String, bool) {
    match art::landscape(&held.arts) {
        Some(art) => (art.to_string(), true),
        None => (held.art.clone(), false),
    }
}

/// The note of an entry, under the blurb on a card and at the right of a
/// thin row: how many episodes the catalog holds for a series run, and
/// the standing of an entry no library holds. A film the catalog holds
/// carries none.
pub fn note(entry: &Entry, today: &str) -> String {
    // A gap the file dates after today reads Coming and as much of the
    // date as the file knows, in words, because the date is what a person
    // waits for, and every other gap reads Missing. A held entry carries
    // no note, because its episodes stand on the facts line.
    match entry.standing(today) {
        Standing::Held => String::new(),
        Standing::Coming => format!("{COMING} {}", facts::date_worded(&entry.released, today)),
        Standing::Missing => MISSING.to_string(),
    }
}

// The word under an entry no library holds and the file does not date
// ahead.
const MISSING: &str = "Missing";

// The word before the year of an entry no library holds and the file
// dates ahead.
const COMING: &str = "Coming";

// One cell as a row of its own, with the time label the calendar gives
// its span.
fn row(entry: &Entry, calendar: &Option<crate::catalog::Calendar>, cell: Cell) -> Row {
    Row {
        cell,
        time: timed(calendar, entry.timed, entry.from, entry.to),
        timed: entry.timed,
        from: entry.from,
        to: entry.to,
    }
}

// The time label of one span: nothing where the entry carries no time,
// and nothing where the file names no calendar, because the numbers
// alone say nothing without one.
fn timed(calendar: &Option<crate::catalog::Calendar>, timed: bool, from: f64, to: f64) -> String {
    match (timed, calendar) {
        (true, Some(calendar)) => calendar.label(from, to),
        _ => String::new(),
    }
}

/// Where every row starts, from the top of the wall, and where the last
/// one ends: one more top than there are rows. The first row starts under
/// `head`, the room the caption and the focus mark need, and every row
/// after it starts under the one before it and the space under that.
/// Every measure of the wall that names a row reads these, because a card
/// and a thin row are not one height.
pub fn tops(rows: &[Row], headings: &[Heading], art: f32, head: f32) -> Vec<f32> {
    let mut tops = Vec::with_capacity(rows.len() + 1);
    let mut top = head;
    for (index, row) in rows.iter().enumerate() {
        top += over(headings, index) as f32 * HEADING;
        tops.push(top);
        top += row.height(art) + GAP;
    }
    tops.push(top);
    tops
}

/// Where one row ends, in the wall's own space: the next row's top, less
/// the headings that stand over the next row and the gap. The tops alone
/// no longer say it, because a heading takes its room between two rows.
pub fn foot(headings: &[Heading], tops: &[f32], row: usize) -> f32 {
    let top = tops.get(row).copied().unwrap_or_default();
    let next = tops.get(row + 1).copied().unwrap_or(top + GAP);
    next - over(headings, row + 1) as f32 * HEADING - GAP
}

/// The top of one heading in the wall's own space: the headings over a
/// row stack right over it, in list order, the first the highest.
pub fn heading_top(headings: &[Heading], index: usize, tops: &[f32]) -> f32 {
    let heading = &headings[index];
    let row = tops.get(heading.first).copied().unwrap_or_default();
    let under = headings[..index]
        .iter()
        .filter(|other| other.first == heading.first)
        .count();
    row - (over(headings, heading.first) - under) as f32 * HEADING
}

/// The box every heading draws in, in frame space after the scroll: at
/// its own top, in the flow of the rows. A heading that scrolls up
/// passes under the held line, which is where its era's name goes on
/// reading.
pub fn heading_boxes(
    lane: Rectangle,
    headings: &[Heading],
    tops: &[f32],
    down: f32,
) -> Vec<Rectangle> {
    (0..headings.len())
        .map(|index| {
            let from = heading_top(headings, index, tops);
            area(lane.x, lane.y + from - down, lane.width, HEADING)
        })
        .collect()
}

/// The one line held at the top of the lane while the wall is inside an
/// era: every era whose heading has scrolled past the top and whose rows
/// still reach under it, outer to inner, "The Infinity Saga › Phase
/// Three · 3 years", the innermost with its count. One line, and never
/// a stack, because two headings of one weight at the top read as two
/// things at once and not as one inside another. Nothing while the wall
/// stands at its top or between eras.
pub fn crumb(headings: &[Heading], tops: &[f32], down: f32) -> Option<String> {
    let inside: Vec<&Heading> = headings
        .iter()
        .enumerate()
        .filter(|(index, heading)| {
            let from = heading_top(headings, *index, tops);
            let end = foot(headings, tops, heading.last) + GAP;
            from < down - SLACK && end > down + SLACK
        })
        .map(|(_, heading)| heading)
        .collect();
    let (last, outer) = inside.split_last()?;
    let mut line = String::new();
    for heading in outer {
        line.push_str(&heading.name);
        line.push_str(CRUMB_MARK);
    }
    line.push_str(&last.label());
    Some(line)
}

/// The band the held line takes at the top of the cards' column: one
/// heading tall while a line holds, and nothing while none does. The
/// band covers the cards alone, so the strip's lines and the time
/// labels beside them run up to the top of the wall, and the rule under
/// the line says where the cards are cut.
pub fn band(cards: Rectangle, held: bool) -> Rectangle {
    let height = match held {
        true => HEADING.min(cards.height),
        false => 0.0,
    };
    area(cards.x, cards.y, cards.width, height)
}

/// The part of the cards' column under the held line, where the rows
/// draw, so no row draws over it.
pub fn under(cards: Rectangle, held: bool) -> Rectangle {
    let height = band(cards, held).height;
    area(
        cards.x,
        cards.y + height,
        cards.width,
        (cards.height - height).max(0.0),
    )
}

/// The part of the wall the strip and the cards draw in: everything to
/// the right of the time column. A wall with no column, whose `time` is
/// no width at all, gives them the whole of it.
pub fn columned(wall: Rectangle, time: f32) -> Rectangle {
    area(
        wall.x + time,
        wall.y,
        (wall.width - time).max(0.0),
        wall.height,
    )
}

/// Whether any row carries a time label, which is what earns the time
/// label its column.
pub fn labelled(rows: &[Row]) -> bool {
    rows.iter().any(|row| !row.time.is_empty())
}

/// The lane of the wall: the wall itself, the part the strip and the
/// cards share, the strip, and the cards. The left-hand room is only
/// what is used: the time label takes its column only where a row
/// carries one, and the strip a pitch for every lane its runs fill.
/// Where none of them stands at the left, the cards keep the width they
/// have beside a time column at its floor and stand centered in the
/// region, so a page of one universe with no calendar does not sit off
/// to the right.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Lane {
    pub wall: Rectangle,
    pub columned: Rectangle,
    pub strip: Rectangle,
    pub cards: Rectangle,
}

impl Lane {
    /// The lane for these runs over the region, beside a time column of
    /// this width.
    pub fn of(region: Rectangle, runs: &[metro::Run], time: f32) -> Self {
        let wall = region;
        let columned = columned(wall, time);
        let strip = area(columned.x, columned.y, metro::width(runs), columned.height);
        let gap = match strip.width > 0.0 {
            true => GAP,
            false => 0.0,
        };
        let bare = time <= 0.0 && strip.width == 0.0;
        let cards = match bare {
            true => area(
                region.x + floor() / 2.0,
                region.y,
                (region.width - floor()).max(0.0),
                region.height,
            ),
            false => area(
                strip.x + strip.width + gap,
                columned.y,
                (columned.width - strip.width - gap).max(0.0),
                columned.height,
            ),
        };
        Self {
            wall,
            columned,
            strip,
            cards,
        }
    }
}

/// The box one row draws in, in frame space after the scroll: from the
/// right of the strip to the right of the wall, as tall as the row. A row
/// past the tops draws nothing.
pub fn cell_box(
    cards: Rectangle,
    row: usize,
    headings: &[Heading],
    tops: &[f32],
    down: f32,
) -> Rectangle {
    let top = tops.get(row).copied().unwrap_or_default();
    area(
        cards.x,
        cards.y + top - down,
        cards.width,
        (foot(headings, tops, row) - top).max(0.0),
    )
}

/// The part of the frame the strip and the rows draw in. It reaches the
/// focus stroke's own width past the strip, the cards, and the first
/// row, so the mark of a focused row is whole wherever the row is.
pub fn clipped(columned: Rectangle) -> Rectangle {
    area(
        columned.x - REACH,
        columned.y,
        columned.width + 2.0 * REACH,
        columned.height,
    )
}

/// The box one row's time label draws in, in frame space after the
/// scroll, at the left of the wall. `time` is the width of the column.
pub fn time_box(
    wall: Rectangle,
    time: f32,
    row: usize,
    headings: &[Heading],
    tops: &[f32],
    offset: f32,
) -> Rectangle {
    let top = tops.get(row).copied().unwrap_or_default();
    area(
        wall.x,
        wall.y + top - offset,
        (time - GAP).max(0.0),
        foot(headings, tops, row) - top,
    )
}

/// The length of the wall these tops lay out, the space over the first
/// row and under the last one included.
pub fn content(tops: &[f32]) -> f32 {
    tops.last().copied().unwrap_or_default() + TAIL
}

/// How far the wall has scrolled with focus on this row, from where it
/// stood before. The wall moves only when it has to: when the focused
/// row has left the view above, the wall comes down until the row stands
/// at the top, under the headings over it and the held line; when it has
/// left below, the wall goes up until the row stands at the foot. A row
/// already in view moves nothing, so up and down walk the rows on the
/// screen and the wall stays still under them. The block the scroll
/// keeps in view is the row and the headings that stand over it when it
/// is at the top. The last row pulls the space under it into view, so
/// the wall stops at its own foot and not a row short of it.
pub fn scroll(before: f32, row: usize, headings: &[Heading], tops: &[f32], height: f32) -> f32 {
    let count = tops.len().saturating_sub(1);
    let top = tops.get(row).copied().unwrap_or_default() - reach(headings, row) as f32 * HEADING;
    let tail = match row + 1 >= count {
        true => TAIL,
        false => 0.0,
    };
    let foot = foot(headings, tops, row) + GAP + tail;
    // A short wall keeps the room to bring its last row out from under
    // the held line, so a wall of two rows and six eras never leaves a
    // title covered.
    let most = (content(tops).max(top + height) - height).max(0.0);
    let down = before.clamp(0.0, most);
    if top < down {
        top.max(0.0)
    } else if foot > down + height {
        (foot - height).clamp(0.0, most)
    } else {
        down
    }
}

#[cfg(test)]
mod tests;
