// The banner reads its titles off the strips and not off the catalog.
// It shows one title from each strip the day drew, then the newest
// release and the newest arrival, so it is a view of the page under it
// and never a random pick. This module holds the titles, the focus, the
// read, the presses, and the backdrop the rest prefetches.

use super::Strip;
use crate::catalog::Source;
use crate::focus;
use crate::screens::{Item, Step, movie, series, slots};

/// The most titles the banner holds. Four drawn strips and two recency
/// strips feed it, and a longer row of indicators reads as noise.
pub const MOST: usize = 6;

/// One title of the banner: the item a select opens, and the words and
/// art paths the frame draws.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Title {
    /// The slot the title came from, which a select opens the page for.
    pub item: Item,
    /// The name a person reads. It is held apart from the item because an
    /// episode slot names its episode, and the banner shows its series.
    pub name: String,
    /// The logo path, empty where the title has none.
    pub logo: String,
    /// The backdrop path, never empty in the banner.
    pub backdrop: String,
    /// The facts line, as the title's page draws it.
    pub facts: String,
    /// The tagline, empty where the sidecar named none.
    pub tagline: String,
}

impl Title {
    // The title of one item, or nothing where its page draws over no art.
    // The details are read by the slot's kind, because an episode and a
    // folded series both open the series' page and show its backdrop.
    fn of(item: &Item, source: &mut dyn Source) -> Option<Self> {
        let series = match (item.kind.as_str(), &item.episode) {
            ("episodes", Some(place)) => Some(place.series.as_str()),
            ("series", _) => Some(item.id.as_str()),
            _ => None,
        };
        let (name, logo, backdrop, facts, tagline) = match series {
            Some(id) => {
                let details = source.series(&item.library, id)?;
                let facts = series::facts_of(&details);
                (
                    details.title,
                    details.logo,
                    details.backdrop,
                    facts,
                    details.tagline,
                )
            }
            None => {
                let details = source.movie(&item.library, &item.id)?;
                let facts = movie::facts_of(&details);
                (
                    details.title,
                    details.logo,
                    details.backdrop,
                    facts,
                    details.tagline,
                )
            }
        };
        if backdrop.is_empty() {
            return None;
        }
        Some(Self {
            item: item.clone(),
            name,
            logo,
            backdrop,
            facts,
            tagline,
        })
    }

    // The library and the id of the page a select opens. An episode and its
    // series open one page, and the banner shows a page once.
    fn page(&self) -> (&str, &str) {
        let id = match &self.item.episode {
            Some(place) => place.series.as_str(),
            None => self.item.id.as_str(),
        };
        (&self.item.library, id)
    }
}

/// The banner: its titles in the page's order, and the one that holds
/// focus.
#[derive(Debug, Default)]
pub struct Banner {
    pub titles: Vec<Title>,
    pub focus: usize,
}

impl Banner {
    /// The first title of each strip whose page draws over art and is not
    /// in the banner yet, at most `MOST`. Each strip gives one title and a
    /// page appears once, because the banner is a row of the strips under
    /// it, and the newest release is often the newest arrival.
    pub fn read<'a>(
        strips: impl Iterator<Item = &'a Strip>,
        source: &mut dyn Source,
    ) -> Vec<Title> {
        let mut titles: Vec<Title> = Vec::new();
        for strip in strips {
            let found = strip.items.iter().find_map(|item| {
                let title = Title::of(item, source)?;
                match titles.iter().any(|held| held.page() == title.page()) {
                    true => None,
                    false => Some(title),
                }
            });
            if let Some(title) = found {
                titles.push(title);
            }
            if titles.len() >= MOST {
                break;
            }
        }
        titles
    }

    /// Take the titles read again and keep focus on the page it was on, or
    /// in range. Focus follows the page and not the index, because a reread
    /// can move a title along the row.
    pub fn reread(&mut self, titles: Vec<Title>) {
        let held = self.titles.get(self.focus).map(|title| {
            let (library, id) = title.page();
            (library.to_string(), id.to_string())
        });
        self.titles = titles;
        self.focus = held
            .and_then(|(library, id)| {
                self.titles
                    .iter()
                    .position(|title| title.page() == (library.as_str(), id.as_str()))
            })
            .unwrap_or(self.focus.min(self.titles.len().saturating_sub(1)));
    }

    pub fn is_empty(&self) -> bool {
        self.titles.is_empty()
    }

    /// The title the frame shows, or nothing while the banner holds
    /// none.
    pub fn focused(&self) -> Option<&Title> {
        self.titles.get(self.focus)
    }

    /// Fold one press in. Left and right move across the titles, and select
    /// opens the current title's page.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if key == "enter" {
            return match self.focused() {
                Some(title) => slots::opened(&title.item, source),
                None => Step::Stay,
            };
        }
        self.focus = focus::row(self.focus, self.titles.len(), key);
        Step::Stay
    }

    /// The library and the backdrop of the current title's page. The
    /// backdrop was read with the title, so the rest costs no read.
    pub fn resting(&self) -> Option<(String, String)> {
        let title = self.focused()?;
        Some((title.item.library.clone(), title.backdrop.clone()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sample::Catalog;
    use crate::screens::home::Home;

    #[test]
    fn the_sample_home_page_opens_on_a_banner_of_titles_with_backdrops() {
        let mut catalog = Catalog;
        let home = Home::open(&mut catalog);
        let banner = home.banner().expect("the home page holds a banner");
        assert!(!banner.is_empty());
        assert!(banner.titles.len() <= MOST);
        assert!(banner.titles.iter().all(|title| !title.backdrop.is_empty()));
        assert!(banner.titles.iter().all(|title| !title.facts.is_empty()));
        assert!(banner.titles.iter().all(|title| !title.name.is_empty()));
        let mut pages: Vec<(&str, &str)> = banner.titles.iter().map(Title::page).collect();
        pages.sort_unstable();
        pages.dedup();
        assert_eq!(pages.len(), banner.titles.len());
        assert_eq!(home.focus, 0);
    }

    #[test]
    fn a_reread_keeps_focus_on_the_page_it_held_or_clamps() {
        let mut catalog = Catalog;
        let home = Home::open(&mut catalog);
        let titles = home.banner().expect("a banner").titles.clone();
        let mut banner = Banner {
            titles: titles.clone(),
            focus: 1,
        };

        let mut moved = titles.clone();
        moved.rotate_left(1);
        banner.reread(moved);
        assert_eq!(banner.focus, 0);
        assert_eq!(banner.focused(), titles.get(1));

        banner.reread(titles[..1].to_vec());
        assert_eq!(banner.focus, 0);

        banner.reread(Vec::new());
        assert_eq!(banner.focus, 0);
        assert_eq!(banner.resting(), None);
    }
}
