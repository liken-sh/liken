// The slots of one query are one piece of code every wall shares. The
// library wall and a person's works were two copies of one grid, and the
// home page's walls would have been a third. This module holds the query,
// the answer's name, the items, the focus, the arrows and select over
// them, the prefetch under focus, and the drawing into a region.
// The select and the prefetch over one item are functions of their own
// here, because the home page's strips open the same pages a wall
// does.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::Rectangle;

use super::wall::Wall;
use super::{Item, Screen, Step, movie, person, series};
use crate::catalog::{Query, Source};
use crate::focus;
use crate::posters::Posters;
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
}

impl Slots {
    /// Read the query's answer, with focus on the first slot.
    pub fn open(query: Query, source: &mut dyn Source) -> Self {
        let mut slots = Self {
            query,
            name: String::new(),
            items: Vec::new(),
            focus: 0,
        };
        slots.reread(source);
        slots
    }

    /// Read the answer again and keep focus in range, because a change can
    /// remove the focused slot.
    pub fn reread(&mut self, source: &mut dyn Source) {
        let answer = source.wall(&self.query);
        self.name = answer.name;
        self.items = answer
            .slots
            .into_iter()
            .map(|slot| Item::of(&self.query, slot))
            .collect();
        self.focus = self.focus.min(self.items.len().saturating_sub(1));
    }

    /// The heading the band draws over these slots: the query's heading
    /// over the name and the count.
    pub fn heading(&self) -> String {
        self.query.heading(&self.name, self.items.len())
    }

    /// Fold one press in. The arrows move across the grid, and select opens
    /// the page for the focused slot's kind.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.focus = focus::wall(self.focus, self.items.len(), wall::COLUMNS, key);
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
    pub fn draw<P: Posters>(
        &self,
        frame: &mut canvas::Frame<Renderer>,
        posters: &mut P,
        region: Rectangle,
        marked: bool,
        lines: usize,
    ) {
        let cells = wall::lined(region.width, wall::POSTER, wall::COLUMNS, lines);
        wall::draw(
            frame,
            posters,
            &wall::Grid {
                items: &self.items,
                focus: Some(self.focus),
                marked,
                library: "",
                ratio: wall::POSTER,
                columns: wall::COLUMNS,
                lines,
                offset: wall::scrolled(
                    self.focus,
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
    Step::Open(Screen::Wall(Wall::open(query.all_titles(), source)))
}

/// The page a select on one item opens, by the item's kind: a series
/// page for a series, the series page focused on the episode for an
/// episode, and a movie page for everything else. Nothing where the
/// catalog no longer holds the item.
pub fn opened(item: &Item, source: &mut dyn Source) -> Step {
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
    use crate::sample::Catalog;

    const LIBRARY: &str = "sample/features";

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
        };
        let Step::Open(Screen::Wall(wall)) = see_all(&query, &mut Catalog) else {
            panic!("a library opens a wall");
        };
        assert_eq!(wall.slots.query, query);
        assert_eq!(wall.head, None);
    }
}
