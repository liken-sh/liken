// The build of an index. Items and people go in one at a time, each
// string folded into links from its words to the item. `finish` sorts
// the links and lays them out as the flat arrays a read walks. A build
// is always whole: the arrays are packed for reading, and inserting
// into them would cost more than building them again.

use std::collections::HashMap;

use super::fold;
use super::rank::{self, Kind, Position, Where};
use super::{Held, Index, Someone, Span};
use crate::catalog::Slot;

// One link while the build runs is a `u64`: the word in the high bits,
// the item's place under it, and the stored rank in the low byte. A
// plain sort of these numbers groups links by word, then by item, with
// the best rank of each pair first, which is the order `finish` needs.
const ITEM_SHIFT: u32 = 8;
const WORD_SHIFT: u32 = 40;
const BELOW_WORD: u64 = (1 << WORD_SHIFT) - 1;

// The high bit marks a person's place while the build runs, because
// people are numbered after the titles and the count of titles is not
// known until the last item is added. `sorted` takes the mark off.
const SOMEONE: u32 = 1 << 31;

/// The place of one item in the index, as `add` answers it, so a caller
/// can fold more strings onto that item later.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Place(pub(super) u32);

/// One person the index can answer with. A person is found by name
/// alone, so nothing is folded onto them later and `person` answers no
/// place.
#[derive(Debug, Clone, Default)]
pub struct Person {
    pub library: String,
    pub path: String,
    pub name: String,
    pub headshot: bool,
}

/// One item the index can answer with: the slot a wall draws, the sort
/// key ties break by, the kind ties order by, and every string a search
/// can land on with the rung each one is.
#[derive(Debug, Clone, Default)]
pub struct Item {
    pub slot: Slot,
    pub sort_key: String,
    pub kind: Kind,
    pub strings: Vec<(Where, String)>,
}

/// The build of one index. An alias or an episode's string arrives after
/// its item, so `add` answers a `Place` and `fold` takes one.
#[derive(Debug, Default)]
pub struct Builder {
    text: String,
    held: HashMap<Box<str>, Span>,
    vocabulary: HashMap<Box<str>, u32>,
    links: Vec<u64>,
    items: Vec<Held>,
    people: Vec<Someone>,
    libraries: Vec<String>,
    kinds: Vec<String>,
    folded: usize,
    buffer: Vec<String>,
}

impl Builder {
    /// A build with nothing in it.
    pub fn new() -> Self {
        Self::default()
    }

    /// Add one item and answer its place.
    pub fn add(&mut self, item: Item) -> Place {
        let place = Place(self.items.len() as u32);
        for (rung, string) in &item.strings {
            self.fold(place, *rung, string);
        }
        let held = Held {
            library: named(&mut self.libraries, &item.slot.library),
            word: named(&mut self.kinds, &item.slot.kind),
            kind: item.kind,
            duration: item.slot.duration as i32,
            seasons: item.slot.seasons as i32,
            id: self.hold(&item.slot.id),
            title: self.hold(&item.slot.title),
            released: self.hold(&item.slot.released),
            art: self.hold(&item.slot.art),
            rating: self.hold(&item.slot.rating),
            tagline: self.hold(&item.slot.tagline),
            sort_key: self.hold(&item.sort_key),
        };
        self.items.push(held);
        place
    }

    /// Add one person, found by their name alone.
    pub fn person(&mut self, person: Person) {
        let place = Place(SOMEONE | self.people.len() as u32);
        self.fold(place, Where::Person, &person.name);
        let someone = Someone {
            library: named(&mut self.libraries, &person.library),
            path: self.hold(&person.path),
            name: self.hold(&person.name),
            headshot: person.headshot,
        };
        self.people.push(someone);
    }

    /// Fold one more string onto an item already added. The sidecar read
    /// streams aliases and episodes in their own passes after the titles,
    /// and both reach their item through the place `add` answered.
    pub fn fold(&mut self, place: Place, rung: Where, text: &str) {
        self.folded += 1;
        let mut words = std::mem::take(&mut self.buffer);
        words.clear();
        fold::fold(text, &mut words);
        let alone = words.len() == 1;
        for (at, word) in words.iter().enumerate() {
            let position = match (alone, at) {
                (true, _) => Position::Alone,
                (_, 0) => Position::First,
                _ => Position::Later,
            };
            let id = self.word(word);
            let rank = rank::stored(rung, position);
            self.links
                .push((id as u64) << WORD_SHIFT | (place.0 as u64) << ITEM_SHIFT | rank as u64);
        }
        self.buffer = words;
    }

    /// The index these items make.
    pub fn finish(mut self) -> Index {
        let words = self.sorted();
        self.links.sort_unstable();
        // One link per word and item, at the best rank. The rank is the
        // low byte, so the sort put the best link of each pair first and
        // dedup keeps that one.
        self.links.dedup_by_key(|link| *link >> ITEM_SHIFT);

        let mut firsts = vec![0u32; words.len() + 1];
        let mut places = Vec::with_capacity(self.links.len());
        let mut ranks = Vec::with_capacity(self.links.len());
        for link in &self.links {
            firsts[(link >> WORD_SHIFT) as usize + 1] += 1;
            places.push(((link & BELOW_WORD) >> ITEM_SHIFT) as u32);
            ranks.push((link & 0xFF) as u8);
        }
        for at in 1..firsts.len() {
            firsts[at] += firsts[at - 1];
        }

        let mut text = self.text;
        text.shrink_to_fit();
        let mut items = self.items;
        items.shrink_to_fit();
        let mut people = self.people;
        people.shrink_to_fit();
        Index {
            text,
            words,
            firsts,
            places,
            ranks,
            items,
            people,
            libraries: self.libraries,
            kinds: self.kinds,
            folded: self.folded,
        }
    }

    // The vocabulary in word order, appended to the arena. Words were
    // numbered in arrival order during the build, so every link is
    // remapped to the word's sorted number here.
    fn sorted(&mut self) -> Vec<Span> {
        let mut order: Vec<(Box<str>, u32)> =
            std::mem::take(&mut self.vocabulary).into_iter().collect();
        order.sort_unstable_by(|one, other| one.0.cmp(&other.0));
        let mut moved = vec![0u32; order.len()];
        let mut words = Vec::with_capacity(order.len());
        for (fresh, (word, was)) in order.iter().enumerate() {
            moved[*was as usize] = fresh as u32;
            words.push(self.append(word));
        }
        // The count of titles is final now, so a marked place becomes
        // its number after the titles.
        let titles = self.items.len() as u32;
        for link in &mut self.links {
            let word = moved[(*link >> WORD_SHIFT) as usize] as u64;
            let mut place = ((*link & BELOW_WORD) >> ITEM_SHIFT) as u32;
            if place & SOMEONE == SOMEONE {
                place = titles + (place & !SOMEONE);
            }
            *link = word << WORD_SHIFT | (place as u64) << ITEM_SHIFT | (*link & 0xFF);
        }
        words
    }

    // One string held once in the arena. Release dates, ratings, and
    // art paths repeat across many rows, so the second and later copies
    // cost only a span.
    fn hold(&mut self, text: &str) -> Span {
        if text.is_empty() {
            return Span::default();
        }
        if let Some(span) = self.held.get(text) {
            return *span;
        }
        let span = self.append(text);
        self.held.insert(text.into(), span);
        span
    }

    // One string appended to the arena.
    fn append(&mut self, text: &str) -> Span {
        let span = Span {
            at: self.text.len() as u32,
            len: text.len() as u32,
        };
        self.text.push_str(text);
        span
    }

    // The number of one folded word while the build runs, in arrival
    // order.
    fn word(&mut self, word: &str) -> u32 {
        if let Some(id) = self.vocabulary.get(word) {
            return *id;
        }
        let id = self.vocabulary.len() as u32;
        self.vocabulary.insert(word.into(), id);
        id
    }
}

// The index of one name in a short list, added if absent. A library name
// or a kind word repeats on every item, so an item carries two bytes and
// the list holds the string once.
fn named(list: &mut Vec<String>, name: &str) -> u16 {
    match list.iter().position(|held| held == name) {
        Some(at) => at as u16,
        None => {
            list.push(name.to_string());
            (list.len() - 1) as u16
        }
    }
}
