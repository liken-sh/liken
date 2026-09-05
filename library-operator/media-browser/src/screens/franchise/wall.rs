// The franchise page's wall, measured before anything draws. The wall is
// one lane of rows in story order, one row per entry, first to last; two
// entries the story tells at once each take a row of their own. The rows
// follow the order and never the times, because a franchise runs from
// 1260 BC to 2028 with most of it in twenty years, and a time scale is
// then one dot and an empty rail. The universes are the lines of the
// metro strip beside the lane: the franchise's own first, then every
// other universe an entry names, in first-seen order. A held entry is a
// card as tall as its art and the gaps around it, and an entry no library
// holds is a thin row, so the rows are not one height, and every measure
// of the wall reads the row tops the wall lays out once.

use iced_winit::core::Rectangle;

use super::metro;
use crate::catalog::franchise::{Entry, Era, Franchise, Held, SERIES, Standing};
use crate::look;
use crate::views::{REACH, area, rail, text, wall};

/// The space under a row, inside a card, and between the strip and the
/// cards.
pub const GAP: f32 = 16.0;

/// The width the time label takes at the left of the wall.
pub const TIME: f32 = 168.0;

/// The height the legend of universes takes over the first row. It holds
/// the legend and the space the mark of a focused entry in the first row
/// reaches into.
pub const HEAD: f32 = 48.0;

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

/// The universes, as the lines of the strip and the items of the legend.
/// The franchise's own is the first, even where the file names none,
/// because an entry with no universes is in it. Every other universe an
/// entry names follows, in first-seen order.
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

/// The eras as the bars of the jump rail. A row is in an era where the row
/// carries a time and its span meets the era's, so the file writes no row and
/// the rail is derived. The widest era takes the outer lane, and an era a
/// wider one holds whole takes the inner one, so a phase nests inside its
/// saga. An era no row meets draws no bar.
pub fn bars(eras: &[Era], rows: &[Row]) -> Vec<rail::Bar> {
    let mut widest: Vec<&Era> = eras.iter().collect();
    widest.sort_by(|one, other| {
        other
            .width()
            .partial_cmp(&one.width())
            .unwrap_or(std::cmp::Ordering::Equal)
    });

    let mut outer: Vec<&Era> = Vec::new();
    let mut bars = Vec::new();
    for era in widest {
        let mut covered = rows
            .iter()
            .enumerate()
            .filter(|(_, row)| row.timed && era.meets(row.from, row.to))
            .map(|(index, _)| index);
        let Some(first) = covered.next() else {
            continue;
        };
        let last = covered.next_back().unwrap_or(first);
        // An era that meets any era already in the outer lane takes the
        // inner one, held whole or not, because two bars in one lane draw
        // over each other, and the earlier one's label, pinned at the top
        // of the region, hides the later one's.
        let lane = usize::from(outer.iter().any(|wider| wider.meets(era.from, era.to)));
        if lane == 0 {
            outer.push(era);
        }
        bars.push(rail::Bar {
            label: era.name.clone(),
            first,
            last,
            lane,
        });
    }
    bars
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

// The facts line: Film or Series, the year, and then a film's running
// time or a series run's episodes where the catalog holds them. A fact
// the entry does not carry leaves no gap and no dot behind.
fn facts(entry: &Entry, held: &Held) -> String {
    let series = entry.kind == SERIES;
    let kind = match series {
        true => "Series",
        false => "Film",
    };
    let held_facts = match (entry.held.is_some(), series) {
        (true, true) => counted(entry.episodes),
        (true, false) => crate::screens::facts::runtime(held.duration),
        (false, _) => String::new(),
    };
    crate::screens::facts::joined(&[kind, &dated(entry, held), &held_facts])
}

// The art one cell draws, and whether it fills the cell's 16:9 box. The
// ladder is the item's own landscape file, then its fanart, then its
// backdrop, which is the name the scanners of this catalog write the same
// 16:9 art under, and then the poster. A poster does not fill a landscape
// box, so the cell says so and the page draws it at its own ratio. A gap
// holds no item and names no art.
fn drawn(held: &Held) -> (String, bool) {
    for name in LANDSCAPE {
        if let Some(art) = held.arts.iter().find(|art| named(art, name)) {
            return (art.clone(), true);
        }
    }
    (held.art.clone(), false)
}

// The names an item's 16:9 art is written under, in the order a cell
// takes them.
const LANDSCAPE: [&str; 3] = ["landscape.jpg", "fanart.jpg", "backdrop.jpg"];

// Whether one art path is the file of this name. The paths are relative
// to the library's volume, so the name is the last part of the path.
fn named(art: &str, name: &str) -> bool {
    art.rsplit('/').next().is_some_and(|file| file == name)
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
        Standing::Coming => format!("{COMING} {}", worded(&entry.released)),
        Standing::Missing => MISSING.to_string(),
    }
}

/// An ISO date at year, month, or day precision as a person reads it:
/// `2027`, `March 2027`, `12 March 2027`. A month past the twelve, or a
/// part that is not a number, reads as the date was written.
pub fn worded(released: &str) -> String {
    let mut parts = released.split('-');
    let year = parts.next().unwrap_or_default();
    let month = parts
        .next()
        .and_then(|month| month.parse::<usize>().ok())
        .and_then(|month| MONTHS.get(month.wrapping_sub(1)));
    let day = parts.next().and_then(|day| day.parse::<u32>().ok());
    match (month, day) {
        (Some(month), Some(day)) => format!("{day} {month} {year}"),
        (Some(month), None) => format!("{month} {year}"),
        _ => released.to_string(),
    }
}

// The months as the note names them.
const MONTHS: [&str; 12] = [
    "January",
    "February",
    "March",
    "April",
    "May",
    "June",
    "July",
    "August",
    "September",
    "October",
    "November",
    "December",
];

// The word under an entry no library holds and the file does not date
// ahead.
const MISSING: &str = "Missing";

// The word before the year of an entry no library holds and the file
// dates ahead.
const COMING: &str = "Coming";

// How many episodes a series run holds, as a person reads it.
fn counted(episodes: i64) -> String {
    match episodes {
        1 => "1 episode".to_string(),
        count => format!("{count} episodes"),
    }
}

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
/// the legend, and every row after it starts under the one before it and
/// the space under that. Every measure of the wall that names a row reads
/// these, because a card and a thin row are not one height.
pub fn tops(rows: &[Row], art: f32) -> Vec<f32> {
    let mut tops = Vec::with_capacity(rows.len() + 1);
    let mut top = HEAD;
    for row in rows {
        tops.push(top);
        top += row.height(art) + GAP;
    }
    tops.push(top);
    tops
}

/// The part of the wall the strip and the cards draw in: everything to
/// the right of the time label's column where some row carries a time
/// label, and the whole wall where none does.
pub fn columned(wall: Rectangle, labelled: bool) -> Rectangle {
    match labelled {
        true => area(
            wall.x + TIME,
            wall.y,
            (wall.width - TIME).max(0.0),
            wall.height,
        ),
        false => wall,
    }
}

/// Whether any row carries a time label, which is what earns the time
/// label its column.
pub fn labelled(rows: &[Row]) -> bool {
    rows.iter().any(|row| !row.time.is_empty())
}

/// The lane of the wall: where the rail leaves off, the part the strip and
/// the cards share, the strip, and the cards. The left-hand room is only
/// what is used: the rail takes lanes only with eras, the time label its
/// column only where a row carries one, and the strip its pitches only
/// with more than one universe. Where none of them stands at the left,
/// the cards keep the width they have beside a time label and stand
/// centered in the region, so a page of one universe with no calendar
/// does not sit off to the right.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Lane {
    pub wall: Rectangle,
    pub columned: Rectangle,
    pub strip: Rectangle,
    pub cards: Rectangle,
}

impl Lane {
    /// The lane for these eras, rows, and this many universes over the
    /// region.
    pub fn of(region: Rectangle, eras: &[rail::Bar], rows: &[Row], universes: usize) -> Self {
        let wall = rail::beside(region, eras);
        let labelled = labelled(rows);
        let columned = columned(wall, labelled);
        let strip = area(
            columned.x,
            columned.y,
            metro::width(universes),
            columned.height,
        );
        let gap = match strip.width > 0.0 {
            true => GAP,
            false => 0.0,
        };
        let bare = eras.is_empty() && !labelled && strip.width == 0.0;
        let cards = match bare {
            true => area(
                region.x + TIME / 2.0,
                region.y,
                (region.width - TIME).max(0.0),
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
pub fn cell_box(cards: Rectangle, row: usize, tops: &[f32], down: f32) -> Rectangle {
    let top = tops.get(row).copied().unwrap_or_default();
    let next = tops.get(row + 1).copied().unwrap_or(top + GAP);
    area(
        cards.x,
        cards.y + top - down,
        cards.width,
        (next - top - GAP).max(0.0),
    )
}

/// The part of the frame the strip and the rows draw in. It starts under
/// the legend, because a row scrolls under it and must not draw over it.
/// It reaches the focus stroke's own width past the strip, the cards, and
/// the first row, so the mark of a focused row is whole wherever the row
/// is.
pub fn clipped(columned: Rectangle) -> Rectangle {
    area(
        columned.x - REACH,
        columned.y + HEAD - REACH,
        columned.width + 2.0 * REACH,
        (columned.height - HEAD + REACH).max(0.0),
    )
}

/// The band the legend draws in, at the top of the lane. It ends where the
/// rows' own clip starts, so nothing draws in both.
pub fn banded(columned: Rectangle) -> Rectangle {
    area(columned.x, columned.y, columned.width, HEAD - REACH)
}

/// The top of the legend band as the wall scrolls under it. The legend
/// scrolls up with the wall until it meets the top of the region, and
/// stays there for the rest of the scroll, the way a section header holds
/// its place.
pub fn pinned(columned: Rectangle, down: f32) -> f32 {
    (columned.y - down).max(columned.y)
}

/// The box one row's time label draws in, in frame space after the
/// scroll, at the left of the wall.
pub fn time_box(wall: Rectangle, row: usize, tops: &[f32], offset: f32) -> Rectangle {
    let top = tops.get(row).copied().unwrap_or_default();
    let next = tops.get(row + 1).copied().unwrap_or(top + GAP);
    area(wall.x, wall.y + top - offset, TIME - GAP, next - top - GAP)
}

/// The length of the wall these tops lay out, the legend and the space
/// under the last row included.
pub fn content(tops: &[f32]) -> f32 {
    tops.last().copied().unwrap_or(HEAD) + TAIL
}

/// How far the wall has scrolled with focus on this row. The last row
/// pulls the space under it into view, so the wall stops at its own
/// foot and not a row short of it.
pub fn scroll(row: usize, tops: &[f32], height: f32) -> f32 {
    let count = tops.len().saturating_sub(1);
    let top = tops.get(row).copied().unwrap_or(HEAD);
    let next = tops.get(row + 1).copied().unwrap_or(top);
    let block = area(0.0, top, 0.0, next - top);
    let tail = match row + 1 >= count {
        true => TAIL,
        false => 0.0,
    };
    crate::views::stack::offset(block, tail, content(tops), height)
}

#[cfg(test)]
mod tests;
