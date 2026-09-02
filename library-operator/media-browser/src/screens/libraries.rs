// The first screen: every library in the catalog, as a list. Which
// libraries a screen shows is an open problem, so until that resource
// exists this screen lists them all.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Element, Length, Theme};

use super::{Screen, Step, wall};
use crate::catalog::{LibraryEntry, Source, library_name};
use crate::focus;
use crate::posters::Posters;
use crate::views::Card;
use crate::views::list::List;

/// One library, as this list draws it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Entry {
    /// The catalog's library column, `namespace/name`.
    pub library: String,
    /// The library's kind, which decides the screen a descent opens.
    pub kind: String,
    /// The name half of the library column.
    pub name: String,
    /// The kind and the count, as the row's second line.
    pub detail: String,
}

impl Entry {
    fn of(entry: LibraryEntry) -> Self {
        Self {
            name: library_name(&entry.library).to_string(),
            detail: format!("{} · {}", entry.kind, entry.items),
            library: entry.library,
            kind: entry.kind,
        }
    }
}

impl Card for Entry {
    fn name(&self) -> &str {
        &self.name
    }

    fn detail(&self) -> &str {
        &self.detail
    }
}

/// The libraries screen: the entries, and the focused one.
#[derive(Debug)]
pub struct Libraries {
    /// The entries in the order the catalog answered them.
    pub entries: Vec<Entry>,
    /// The focused entry's index.
    pub focus: usize,
}

impl Libraries {
    /// Read the libraries, with focus on the first of them.
    pub fn new(source: &mut dyn Source) -> Self {
        Self {
            entries: read(source),
            focus: 0,
        }
    }

    /// Read the libraries again and keep focus in range, because a
    /// change can remove the focused library.
    pub fn reread(&mut self, source: &mut dyn Source) {
        self.entries = read(source);
        self.focus = self.focus.min(self.entries.len().saturating_sub(1));
    }

    /// Fold one press in. Select opens the library's wall. Back at this
    /// screen never reaches here, because the browser answers it.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        if key != "enter" {
            self.focus = focus::list(self.focus, self.entries.len(), key);
            return Step::Stay;
        }
        let Some(entry) = self.entries.get(self.focus) else {
            return Step::Stay;
        };
        Step::Open(Screen::Wall(wall::Wall::open(
            &entry.library,
            &entry.kind,
            source,
        )))
    }

    /// The view, a list with no art beside its rows.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        canvas(List {
            rows: &self.entries,
            focus: self.focus,
            library: "",
            posters,
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
    }
}

fn read(source: &mut dyn Source) -> Vec<Entry> {
    source.libraries().into_iter().map(Entry::of).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_entry_shows_the_name_half_and_the_count() {
        let entry = Entry::of(LibraryEntry {
            library: "screening/features".into(),
            kind: "movies".into(),
            items: 42,
        });
        assert_eq!(entry.name, "features");
        assert_eq!(entry.library, "screening/features");
        assert_eq!(entry.detail, "movies · 42");
        assert_eq!(entry.kind, "movies");
    }

    #[test]
    fn an_entry_without_a_namespace_keeps_its_whole_name() {
        let entry = Entry::of(LibraryEntry {
            library: "features".into(),
            kind: "movies".into(),
            items: 1,
        });
        assert_eq!(entry.name, "features");
    }
}
