// The slots of one query are one piece of code every wall shares. The
// library wall and a person's works were two copies of one grid, and the
// home page's walls would have been a third. This module holds the query,
// the answer's name, the items, the focus, the arrows and select over
// them, the prefetch under focus, and the drawing into a region.
// The select and the prefetch over one item are functions of their own
// here, because the home page's strips open the same pages a wall
// does.

use std::ops::Range;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::Rectangle;

use super::wall::Wall;
use super::{Item, Screen, Step, credits, franchise, movie, person, series};
use crate::art::Art;
use crate::catalog::search::PEOPLE;
use crate::catalog::{Counts, Query, Source};
use crate::focus;
use crate::views::wall;

/// The slots one query answered: the query, the name the answer
/// carried, the items in the answer's order, and the focused item's
/// index.
#[derive(Debug)]
pub struct Slots {
    pub query: Query,
    pub name: String,
    pub items: Vec<Item>,
    pub focus: usize,
    // The run of items whose cards the shaper has already cut. A wall of
    // a whole library is thousands of slots, and cutting them all costs
    // more than a press may take, so the read cuts the page around the
    // focus and a move cuts what it reaches.
    cut: Range<usize>,
}

impl Slots {
    /// Read the query's answer, with focus on the first slot.
    pub fn open(query: Query, source: &mut dyn Source) -> Self {
        let mut slots = Self {
            query,
            name: String::new(),
            items: Vec::new(),
            focus: 0,
            cut: 0..0,
        };
        slots.reread(source);
        slots
    }

    /// Read the answer again and keep focus in range, because a change can
    /// remove the focused slot.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let mut answer = source.wall(&self.query);
        credits::credit(&self.query, &mut answer.slots);
        self.name = answer.name;
        self.items = answer
            .slots
            .into_iter()
            .map(|slot| Item::of(&self.query, slot))
            .collect();
        self.cut = 0..0;
        self.focus = self.focus.min(self.items.len().saturating_sub(1));
        self.stand(self.focus);
    }

    /// Cut the cards of the page around this slot. The rail scrolls the
    /// wall to rows the focus has not reached, and every card a frame
    /// draws is cut at the read and never on the frame.
    pub fn stand(&mut self, index: usize) {
        self.fitted(index);
    }

    // Cut the cards of the page around one slot to the band one cell
    // holds, and leave the cards already cut as they are.
    fn fitted(&mut self, index: usize) {
        let page = page(index, self.items.len());
        let band = wall::band(wall::COLUMNS);
        let cut = self.cut.clone();
        for index in page.clone().filter(|index| !cut.contains(index)) {
            self.items[index].fit(band);
        }
        self.cut = match cut.is_empty() || page.start > cut.end || cut.start > page.end {
            true => page,
            false => page.start.min(cut.start)..page.end.max(cut.end),
        };
    }

    /// The heading the band draws over these slots: the query's heading
    /// over the name and the count.
    pub fn heading(&self) -> String {
        let kinds = self.items.iter().map(|item| item.kind.as_str());
        self.query.heading(&self.name, Counts::of(kinds))
    }

    /// Fold one press in. The arrows move across the grid, and select opens
    /// the page for the focused slot's kind.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.focus = focus::wall(self.focus, self.items.len(), wall::COLUMNS, key);
            self.fitted(self.focus);
            return Step::Stay;
        }
        match self.items.get(self.focus) {
            Some(item) => opened(item, source),
            None => Step::Stay,
        }
    }

    /// The library and the backdrop of the page the focused slot opens, by
    /// the slot's kind, so the store decodes it while focus rests. Nothing
    /// where that page draws over no art.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        backdrop(self.items.get(self.focus)?, source)
    }

    /// Draw the grid of these slots in the region, scrolled so the focused
    /// row stays in view, with the mark on the focused slot only while
    /// `marked`, and `lines` caption lines under each slot.
    pub fn draw<A: Art>(
        &self,
        frame: &mut canvas::Frame<Renderer>,
        store: &mut A,
        region: Rectangle,
        marked: bool,
        lines: usize,
    ) {
        self.draw_at(frame, store, region, self.focus, marked, lines);
    }

    /// The same drawing, with the grid standing at the slot the caller
    /// names instead of the focus. A wall whose rail holds focus stands
    /// at the slot a select on the focused bar lands on, so the wall
    /// follows the bar.
    pub fn draw_at<A: Art>(
        &self,
        frame: &mut canvas::Frame<Renderer>,
        store: &mut A,
        region: Rectangle,
        standing: usize,
        marked: bool,
        lines: usize,
    ) {
        let cells = wall::lined(region.width, wall::POSTER, wall::COLUMNS, lines);
        wall::draw(
            frame,
            store,
            &wall::Grid {
                items: &self.items,
                focus: Some(standing),
                marked,
                library: "",
                ratio: wall::POSTER,
                columns: wall::COLUMNS,
                lines,
                offset: wall::scrolled(
                    standing,
                    self.items.len(),
                    wall::COLUMNS,
                    &cells,
                    region.height,
                ),
                region,
            },
        );
    }
}

// How many rows of cards on each side of the focused one the shaper
// cuts. It is far more than a screen holds, so a move of one row cuts one
// row, and a frame never draws a card the shaper has not measured.
const PAGE: usize = 12;

// The run of items one focus asks the shaper for: the rows around the
// focused one, clamped to the wall.
fn page(focus: usize, count: usize) -> Range<usize> {
    let row = focus / wall::COLUMNS;
    let first = row.saturating_sub(PAGE) * wall::COLUMNS;
    first..((row + PAGE + 1) * wall::COLUMNS).min(count)
}

/// The screen a "see all" on this query opens. A person opens their own
/// page, which draws the headshot and the dates over the same works.
/// Every other query opens the wall of everything it answers, and a
/// query with a page of its own is that page, because the wall carries
/// its head. Nothing where the catalog no longer holds the person.
pub fn see_all(query: &Query, source: &mut dyn Source) -> Step {
    if let Query::Person { library, path } = query {
        return match person::Person::open(library, path, source) {
            Some(page) => Step::Open(Screen::Person(Box::new(page))),
            None => Step::Stay,
        };
    }
    Step::Open(Screen::Wall(Box::new(Wall::open(
        query.all_titles(),
        source,
    ))))
}

/// The kind word a franchise slot carries, the one kind that opens a
/// franchise page. The home page's franchises strip and a search hit both
/// carry it.
pub const FRANCHISE: &str = "franchise";

/// The kind word a set's slot carries. A search answers one, and it
/// opens the wall of the set's members.
pub const SETS: &str = "sets";

/// The page a select on one item opens, by the item's kind: a series
/// page for a series, the series page focused on the episode for an
/// episode, and a movie page for everything else. A search also answers
/// three kinds no other wall holds: a set opens the wall of its members,
/// a franchise opens its page, and a person opens theirs. Nothing where
/// the catalog no longer holds the item.
pub fn opened(item: &Item, source: &mut dyn Source) -> Step {
    if item.kind == SETS {
        let query = Query::Set {
            library: item.library.clone(),
            id: item.id.clone(),
        };
        return Step::Open(Screen::Wall(Box::new(Wall::open(query, source))));
    }
    let page = match (item.kind.as_str(), &item.episode) {
        ("episodes", Some(place)) => series::Series::open_at(
            &item.library,
            &place.series,
            (place.season, place.episode),
            source,
        )
        .map(|page| Screen::Series(Box::new(page))),
        ("series", _) => series::Series::open(&item.library, &item.id, source)
            .map(|page| Screen::Series(Box::new(page))),
        (FRANCHISE, _) => franchise::Franchise::open(&item.library, &item.id, source)
            .map(|page| Screen::Franchise(Box::new(page))),
        (PEOPLE, _) => person::Person::open(&item.library, &item.id, source)
            .map(|page| Screen::Person(Box::new(page))),
        _ => movie::Movie::open(&item.library, &item.id, source)
            .map(|page| Screen::Movie(Box::new(page))),
    };
    match page {
        Some(page) => Step::Open(page),
        None => Step::Stay,
    }
}

/// The library and the backdrop of the page a select on this item
/// opens, so the store decodes it while focus rests. An episode opens its
/// series' page. Nothing where that page draws over no art.
pub fn backdrop(item: &Item, source: &mut dyn Source) -> Option<(String, String)> {
    let backdrop = match (item.kind.as_str(), &item.episode) {
        ("episodes", Some(place)) => source.series(&item.library, &place.series)?.backdrop,
        ("series", _) => source.series(&item.library, &item.id)?.backdrop,
        _ => source.movie(&item.library, &item.id)?.backdrop,
    };
    if backdrop.is_empty() {
        return None;
    }
    Some((item.library.clone(), backdrop))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Sort;
    use crate::sample::Catalog;

    const LIBRARY: &str = "sample/features";

    #[test]
    fn the_shaper_cuts_the_rows_around_the_focus_and_no_more() {
        assert_eq!(page(0, 5_000), 0..(PAGE + 1) * wall::COLUMNS);
        assert_eq!(page(0, 12), 0..12);
        let far = page(100 * wall::COLUMNS, 5_000);
        assert_eq!(far.start, (100 - PAGE) * wall::COLUMNS);
        assert_eq!(far.end, (100 + PAGE + 1) * wall::COLUMNS);
        assert_eq!(page(0, 0), 0..0);
    }

    #[test]
    fn a_move_across_the_wall_cuts_the_row_it_reached_and_leaves_the_rest() {
        let query = Query::Library {
            library: LIBRARY.into(),
            sort: Sort::default(),
        };
        let mut slots = Slots::open(query, &mut Catalog);
        assert_eq!(slots.cut, page(0, slots.items.len()));
        slots.key("down", &mut Catalog);
        assert_eq!(slots.cut, 0..page(slots.focus, slots.items.len()).end);
        assert_eq!(slots.cut.end, (PAGE + 2) * wall::COLUMNS);
    }

    // The first hit of one search of the invented catalog, which
    // searches for real.
    fn hit(text: &str, kind: &str) -> Item {
        let query = Query::Search {
            text: text.to_string(),
        };
        let slots = Slots::open(query, &mut Catalog);
        slots
            .items
            .into_iter()
            .find(|item| item.kind == kind)
            .unwrap_or_else(|| panic!("{text} answers a {kind}"))
    }

    #[test]
    fn a_set_a_search_answers_opens_the_wall_of_its_members() {
        let item = hit("specimen cycle", SETS);

        let Step::Open(Screen::Wall(wall)) = opened(&item, &mut Catalog) else {
            panic!("a set opens a wall");
        };

        assert_eq!(
            wall.slots.query,
            Query::Set {
                library: item.library,
                id: item.id,
            }
        );
        assert!(!wall.slots.items.is_empty());
    }

    #[test]
    fn a_franchise_a_search_answers_opens_its_page() {
        let item = hit("marsh", FRANCHISE);

        let Step::Open(Screen::Franchise(page)) = opened(&item, &mut Catalog) else {
            panic!("a franchise opens its page");
        };

        assert_eq!(page.title, "The Marsh Cycle");
    }

    #[test]
    fn a_person_a_search_answers_opens_their_page() {
        let item = hit("player 0001-1", PEOPLE);

        let Step::Open(Screen::Person(page)) = opened(&item, &mut Catalog) else {
            panic!("a person opens their page");
        };

        assert_eq!(page.name, item.name);
        assert_eq!(page.path, item.id);
    }

    #[test]
    fn see_all_on_an_unknown_person_opens_nothing() {
        let query = Query::Person {
            library: LIBRARY.into(),
            path: "nobody".into(),
        };
        assert!(matches!(see_all(&query, &mut Catalog), Step::Stay));
    }

    #[test]
    fn see_all_on_a_library_opens_a_wall_with_no_head() {
        let query = Query::Library {
            library: LIBRARY.into(),
            sort: Sort::default(),
        };
        let Step::Open(Screen::Wall(wall)) = see_all(&query, &mut Catalog) else {
            panic!("a library opens a wall");
        };
        assert_eq!(wall.slots.query, query);
        assert_eq!(wall.heading, wall.slots.heading());
    }
}
