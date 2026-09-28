// The wall's own rail: which walls draw one, what its bars say, and
// where the sort button goes over them. The bars are the wall's order in
// the finest unit whose labels fit the screen, so a person crosses
// thousands of titles in two presses.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::Rectangle;

use crate::catalog::{GenreSort, Query, Sort};
use crate::look;
use crate::screens::{Item, facts};
use crate::views::{area, mark, rail, rounded, text, wall};

use crate::views::rail::Bar;

/// One bar of the wall's rail: the bar the primitive draws, whose
/// `first` and `last` are rows, and the first item the bar covers, which
/// a select on it lands on. A letter range starts in the middle of a row,
/// so the row's own first item is not the bar's.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Jump {
    pub bar: Bar,
    pub item: usize,
}

/// How many screens of wall draw no rail. A wall this short is walked
/// faster than jumped.
pub const SCREENS: usize = 4;

/// How many rows of the wall one screen holds.
pub const ROWS: usize = 2;

// The height of the screen the browser is drawn for. The bars are built
// at the read, before any frame exists, so they are fitted at this
// height the way the series page fits its own.
const SCREEN: f32 = 1080.0;

/// Whether a wall this many rows long draws a rail.
pub fn shows(rows: usize) -> bool {
    rows > SCREENS * ROWS
}

/// How many rows this many items fill.
pub fn rows(items: usize) -> usize {
    items.div_ceil(wall::COLUMNS)
}

/// The region the bars are fitted against at the read, which is
/// the wall's own region on a screen of [`SCREEN`]. A shorter window
/// draws the same bars in a shorter rail.
pub fn region() -> Rectangle {
    super::region(area(0.0, 0.0, 0.0, SCREEN), 0.0)
}

/// How many cells the rail's focus walks: one for each bar, and
/// one more for the sort button where the wall draws one.
pub fn cells(bars: &[Jump], query: &Query) -> usize {
    bars.len() + first(query)
}

/// The cell the first bar takes: the second on a wall that draws
/// a sort button, and the first on a wall that draws none.
pub fn first(query: &Query) -> usize {
    usize::from(query.sort_word().is_some())
}

/// The bar one cell of the rail names, and nothing for the cell
/// the sort button takes.
pub fn barred_at(cell: usize, query: &Query) -> Option<usize> {
    cell.checked_sub(first(query))
}

/// The bar that covers one row of the wall, which a right press
/// from that row moves onto.
pub fn covering(bars: &[Jump], row: usize) -> Option<usize> {
    bars.iter()
        .position(|jump| jump.bar.first <= row && row <= jump.bar.last)
}

/// The part of the region the wall's own slots draw in, with the
/// rail's lane taken off the right of it.
pub fn beside(region: Rectangle, bars: &[Jump]) -> Rectangle {
    rail::beside_at(region, &drawn(bars), rail::Side::Right)
}

// The bars alone, which is what the rail primitive draws.
fn drawn(bars: &[Jump]) -> Vec<Bar> {
    bars.iter().map(|jump| jump.bar.clone()).collect()
}

/// Draw the rail at the right of the region: the sort button over
/// the bars, with the mark on the cell that holds focus.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    region: Rectangle,
    bars: &[Jump],
    query: &Query,
    focus: Option<usize>,
) {
    if bars.is_empty() {
        return;
    }
    if let Some(word) = query.sort_word() {
        pressed(frame, button(region, query), word, focus == Some(0));
    }
    rail::draw_at(
        frame,
        under_button(region, query),
        &drawn(bars),
        focus.and_then(|cell| barred_at(cell, query)),
        rail::Side::Right,
        rail::Fit::Fitted,
    );
}

// The sort button in its own cell, on the lighter ground with full ink,
// so it reads as a control and not as one more bar.
fn pressed(frame: &mut canvas::Frame<Renderer>, cell: Rectangle, word: &str, focused: bool) {
    let bounds = rail::fitted(cell, &[Bar::default()], rail::Side::Right)
        .pop()
        .unwrap_or(cell);
    frame.fill(&rounded(bounds, ROUND), look::track());
    let shown = text::cut(word, look::HEADING, bounds.height);
    text::downward(frame, &shown, bounds, look::HEADING, look::text());
    if focused {
        mark(frame, bounds);
    }
}

// The radius the button is drawn with, the same as a bar's.
const ROUND: f32 = 8.0;

/// The box the sort button draws in, over the bars, and no height
/// at all on a wall that draws no button.
pub fn button(region: Rectangle, query: &Query) -> Rectangle {
    area(region.x, region.y, region.width, taken(region, query))
}

/// The part of the region the bars draw in: under the button and
/// the space that keeps the bars off it on a wall that has one, and the
/// whole of it on a wall that has none.
pub fn under_button(region: Rectangle, query: &Query) -> Rectangle {
    let taken = taken(region, query) + gap(query);
    area(
        region.x,
        region.y + taken,
        region.width,
        (region.height - taken).max(0.0),
    )
}

// The space between the button and the first bar, so the button reads
// as one control and the bars under it as one list.
const BUTTON_GAP: f32 = 24.0;

fn gap(query: &Query) -> f32 {
    match query.sort_word() {
        Some(_) => BUTTON_GAP,
        None => 0.0,
    }
}

// The height the sort button takes off the region: one bar of a rail
// sized for the button's own word, so the word reads whole and the bars
// under it are fitted to what is left.
fn taken(region: Rectangle, query: &Query) -> f32 {
    match query.sort_word() {
        Some(word) => region.height / rail::fits(region, word) as f32,
        None => 0.0,
    }
}

/// The bars of the rail beside these items, in the query's own
/// order, and none where the wall is short or the query draws no rail.
/// `region` is the whole region the wall and the rail share; the bars
/// are fitted to what the sort button leaves of it.
pub fn bars(items: &[Item], query: &Query, region: Rectangle) -> Vec<Jump> {
    let rows = rows(items.len());
    if !shows(rows) {
        return Vec::new();
    }
    let under = under_button(region, query);
    let units = match order(query) {
        Some(Order::Release) => dated(items, under),
        Some(Order::Title) => lettered(items, under),
        Some(Order::Leads) => leads(items, query.name("")),
        None => return Vec::new(),
    };
    barred(&units, rows)
}

// The three shapes a wall's order takes on the rail.
enum Order {
    Release,
    Title,
    Leads,
}

// Which shape one query's order takes, or none on a wall that draws no
// rail.
fn order(query: &Query) -> Option<Order> {
    match query {
        Query::Library { sort, .. } => Some(sorted(*sort)),
        Query::Genre {
            sort: GenreSort::Leads,
            ..
        } => Some(Order::Leads),
        Query::Genre {
            sort: GenreSort::By(sort),
            ..
        } => Some(sorted(*sort)),
        Query::Released { .. } | Query::Added { .. } => Some(Order::Release),
        _ => None,
    }
}

fn sorted(sort: Sort) -> Order {
    match sort {
        Sort::Title => Order::Title,
        Sort::Newest | Sort::Oldest => Order::Release,
    }
}

// One stretch of the wall's order before it is measured in rows:
// what it is called, where it starts, and how many items it holds.
#[derive(Debug, Clone, PartialEq, Eq)]
struct Unit {
    label: String,
    first: usize,
    count: usize,
}

// The units of a wall in release order: years while the years
// fit the region, decades where they do not, and even ranges of decades
// where the decades do not fit either.
fn dated(items: &[Item], region: Rectangle) -> Vec<Unit> {
    let years = merged(runs(items, |item| facts::year(&item.released).to_string()));
    if years.len() <= rail::fits(region, longest(&years)) {
        return years;
    }
    fitted(merged(runs(items, |item| decade(&item.released))), region)
}

// The units of a wall in title order: single letters while the
// letters fit the region, and even ranges of them where they do not.
fn lettered(items: &[Item], region: Rectangle) -> Vec<Unit> {
    fitted(merged(runs(items, |item| letter(&item.name))), region)
}

// These units folded into as many bars as the region holds. A
// range's label is longer than one unit's, and a longer label fits
// fewer bars, so the count is asked again until the labels of the count
// answered fit the region.
fn fitted(units: Vec<Unit>, region: Rectangle) -> Vec<Unit> {
    let mut count = units.len();
    loop {
        let held = ranged(&units, count);
        let fits = rail::fits(region, longest(&held));
        if fits >= count {
            return held;
        }
        count = fits;
    }
}

// The longest label of these units, which is the one the rail has
// to hold.
fn longest(units: &[Unit]) -> &str {
    units
        .iter()
        .max_by_key(|unit| unit.label.chars().count())
        .map(|unit| unit.label.as_str())
        .unwrap_or_default()
}

// The two units of a genre wall in its leading order: the run
// that leads with the genre, then the rest.
fn leads(items: &[Item], genre: String) -> Vec<Unit> {
    let boundary = restarts(items).unwrap_or(items.len());
    let mut units = Vec::new();
    if boundary > 0 {
        units.push(Unit {
            label: format!("primarily {genre}"),
            first: 0,
            count: boundary,
        });
    }
    if boundary < items.len() {
        units.push(Unit {
            label: format!("other {genre}"),
            first: boundary,
            count: items.len() - boundary,
        });
    }
    units
}

// Where the leading run ends. The order is two runs, each newest first,
// so the first item whose release is later than the one before it is
// the first item that does not lead with the genre. The rank the read
// ordered by does not reach the wall, so this is what the wall can see
// of it; it is wrong only when the second run's newest title is older
// than the first run's oldest.
fn restarts(items: &[Item]) -> Option<usize> {
    items
        .windows(2)
        .position(|pair| pair[1].released > pair[0].released)
        .map(|index| index + 1)
}

// The runs of items that share one word, in the wall's own order.
fn runs<K: Fn(&Item) -> String>(items: &[Item], key: K) -> Vec<Unit> {
    let mut units: Vec<Unit> = Vec::new();
    for (index, item) in items.iter().enumerate() {
        let label = key(item);
        match units.last_mut() {
            Some(unit) if unit.label == label => unit.count += 1,
            _ => units.push(Unit {
                label,
                first: index,
                count: 1,
            }),
        }
    }
    units
}

// The units with every unit of less than one row folded into its
// neighbour, so no bar jumps to a row another bar already holds. A unit
// the catalog named nothing folds the same way.
fn merged(units: Vec<Unit>) -> Vec<Unit> {
    let mut kept: Vec<Unit> = Vec::new();
    for unit in units {
        match kept.last_mut() {
            Some(last) if thin(&unit) => last.count += unit.count,
            _ => kept.push(unit),
        }
    }
    while kept.len() > 1 && thin(&kept[0]) {
        let next = kept.remove(1);
        kept[0].count += next.count;
        kept[0].label = next.label;
    }
    kept
}

fn thin(unit: &Unit) -> bool {
    unit.count < wall::COLUMNS || unit.label.is_empty()
}

// This many units folded into this many even ranges, each one
// named by the first and the last unit it holds.
fn ranged(units: &[Unit], slots: usize) -> Vec<Unit> {
    let count = slots.max(1).min(units.len());
    (0..count)
        .map(|index| {
            let start = index * units.len() / count;
            let end = (index + 1) * units.len() / count;
            let first = &units[start];
            let last = &units[end - 1];
            Unit {
                label: spanned(&first.label, &last.label),
                first: first.first,
                count: last.first + last.count - first.first,
            }
        })
        .collect()
}

// What a range of units is called, and the one word where the
// range holds one unit.
fn spanned(first: &str, last: &str) -> String {
    match first == last {
        true => first.to_string(),
        false => format!("{first}\u{2013}{last}"),
    }
}

// The units as bars of the rail: every bar starts on the row its
// unit starts on, no two bars start on one row, and the last bar runs to
// the foot of the wall, so the bars cover every row and no row twice.
fn barred(units: &[Unit], rows: usize) -> Vec<Jump> {
    let mut bars: Vec<Jump> = Vec::new();
    for unit in units {
        let start = match bars.last() {
            Some(last) => (unit.first / wall::COLUMNS).max(last.bar.first + 1),
            None => 0,
        };
        if start >= rows {
            continue;
        }
        if let Some(last) = bars.last_mut() {
            last.bar.last = start - 1;
        }
        bars.push(Jump {
            bar: Bar {
                label: unit.label.clone(),
                first: start,
                last: rows - 1,
                lane: 0,
            },
            item: unit.first.max(start * wall::COLUMNS),
        });
    }
    bars
}

// The decade one release date falls in, and nothing where the
// catalog holds no date.
fn decade(released: &str) -> String {
    let year = facts::year(released);
    match year.len() {
        4 => format!("{}0s", &year[..3]),
        _ => String::new(),
    }
}

// The letter one title is listed under: the first letter of the sort
// key, which is the title without its leading article, folded to upper
// case. A title that starts with anything but a letter is listed under the
// one word every other first character shares.
fn letter(title: &str) -> String {
    let key = keyed(title);
    match key.chars().next() {
        Some(first) if first.is_ascii_alphabetic() => first.to_ascii_uppercase().to_string(),
        _ => OTHER.to_string(),
    }
}

// The label a title is listed under when its first character is not a
// letter.
const OTHER: &str = "#";

// The title without its leading article, which is what the
// catalog sorts a title order by.
fn keyed(title: &str) -> &str {
    for article in ["The ", "A ", "An "] {
        if title
            .get(..article.len())
            .is_some_and(|head| head.eq_ignore_ascii_case(article))
        {
            return &title[article.len()..];
        }
    }
    title
}

#[cfg(test)]
mod tests;
