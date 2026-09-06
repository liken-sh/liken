// The in-memory index that answers the `Search` query. Corrosion's
// schema allows only tables and indexes, so no FTS5 table replicates,
// and a second database file next to the replica would be one more
// thing to be stale or torn on a one-gigabyte machine. The catalog is
// thousands of titles, so a folded copy of every searchable string fits
// in a few megabytes and a read over all of it takes milliseconds.
//
// The index is built whole from the replica and replaced whole. A
// partial update would be a second copy of the catalog's state with its
// own bugs, and a whole build is cheap enough to run after every quiet
// period on the updates feed.

use std::mem::size_of;

use crate::catalog::Slot;

// The build: items and people go in one at a time, and `finish` lays
// them out as the flat arrays a read walks.
mod build;

// The fold: what every string and every query text is reduced to
// before they are compared.
mod fold;

// The rank: where a match landed and how it matched, as the byte a read
// orders hits by.
mod rank;

pub use build::{Builder, Item, Person, Place};
pub use rank::{Kind, Where};

/// The kind word a person's slot carries. The wall reads it to open the
/// person's page instead of a title's.
pub const PEOPLE: &str = "people";

/// The file name of a person's headshot under their path. The index
/// holds a flag and derives the art path at the read, because holding
/// the path for every person cost about 5 MB at the scale test's size.
pub const HEADSHOT: &str = "headshot.jpg";

/// One string in the text arena: its byte offset and its length. Every
/// string the index holds is a span, so a title costs eight bytes plus
/// its text and no allocation of its own.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
struct Span {
    at: u32,
    len: u32,
}

// One title the index answers with: enough of a `Slot` to draw its
// card. The library and the kind word repeat across thousands of rows,
// so each is an index into a short list and costs two bytes.
#[derive(Debug, Clone, Copy)]
struct Held {
    id: Span,
    title: Span,
    released: Span,
    art: Span,
    rating: Span,
    tagline: Span,
    sort_key: Span,
    duration: i32,
    seasons: i32,
    library: u16,
    word: u16,
    kind: Kind,
}

// One person the index answers with. A person has no release, runtime,
// or rating, so a `Held` for each of 33,000 contributors would waste
// most of its bytes. People are a second list after the titles, and one
// numbering runs over both.
#[derive(Debug, Clone, Copy)]
struct Someone {
    path: Span,
    name: Span,
    library: u16,
    headshot: bool,
}

/// How large the index is. `entries` counts the strings folded in,
/// `items` the titles and people, `words` the vocabulary, and `bytes`
/// the memory the arrays hold. The stats line reports it.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Size {
    pub entries: usize,
    pub items: usize,
    pub words: usize,
    pub bytes: usize,
}

/// The index. Every string is a span into one text arena, and every
/// link from a word to an item is a `u32` place and a `u8` rank in two
/// parallel arrays. Flat arrays keep the index at a few bytes per link
/// and let a read walk them with no pointer chasing.
#[derive(Debug, Default)]
pub struct Index {
    text: String,
    words: Vec<Span>,
    // The offset where each word's links start in `places` and `ranks`.
    // It has one entry more than `words`, so the links of word `n` are
    // always `firsts[n]..firsts[n + 1]`.
    firsts: Vec<u32>,
    places: Vec<u32>,
    ranks: Vec<u8>,
    items: Vec<Held>,
    people: Vec<Someone>,
    libraries: Vec<String>,
    kinds: Vec<String>,
    folded: usize,
}

impl Index {
    /// The ranked hits for this text. Every word of the text must match
    /// the same item, so "batman 1989" narrows instead of widening. Text
    /// that folds to no words answers nothing.
    pub fn find(&self, text: &str) -> Vec<Slot> {
        let asked = fold::words(text);
        if asked.is_empty() {
            return Vec::new();
        }
        let mut found = Found::new(self.count());
        for (turn, word) in asked.iter().enumerate() {
            self.walk(word, turn as u32, &mut found);
        }
        let mut hits: Vec<usize> = (0..self.count())
            .filter(|item| found.hits[*item] as usize == asked.len())
            .collect();
        for item in &hits {
            found.close(*item);
        }
        hits.sort_by(|one, other| self.order(&found, *one, *other));
        hits.into_iter().map(|item| self.slot(item)).collect()
    }

    /// The size of the index, counted from the arrays' capacities.
    pub fn size(&self) -> Size {
        Size {
            entries: self.folded,
            items: self.count(),
            words: self.words.len(),
            bytes: self.text.capacity()
                + self.words.capacity() * size_of::<Span>()
                + self.firsts.capacity() * size_of::<u32>()
                + self.places.capacity() * size_of::<u32>()
                + self.ranks.capacity()
                + self.items.capacity() * size_of::<Held>()
                + self.people.capacity() * size_of::<Someone>()
                + self
                    .libraries
                    .iter()
                    .chain(&self.kinds)
                    .map(|name| name.capacity() + size_of::<String>())
                    .sum::<usize>(),
        }
    }

    // Fold every match of one query word into the tally. The scan is
    // over the whole vocabulary because the inside-a-word rung has no
    // order that groups its matches. At 80,000 words the scan takes
    // about 10 ms in a debug build.
    fn walk(&self, asked: &str, turn: u32, found: &mut Found) {
        for (word, span) in self.words.iter().enumerate() {
            let Some(relation) = rank::relate(self.slice(*span), asked) else {
                continue;
            };
            let links = self.firsts[word] as usize..self.firsts[word + 1] as usize;
            for link in links {
                let item = self.places[link] as usize;
                let stored = self.ranks[link];
                found.mark(item, turn, rank::found(stored, relation), stored);
            }
        }
    }

    // The order two hits stand in: the rank, then the kind, then the
    // newest release, then the sort key.
    fn order(&self, found: &Found, one: usize, other: usize) -> std::cmp::Ordering {
        found.best[one]
            .cmp(&found.best[other])
            .then_with(|| self.ties(found, one).cmp(&self.ties(found, other)))
            .then_with(|| self.released(other).cmp(self.released(one)))
            .then_with(|| self.sort_key(one).cmp(self.sort_key(other)))
    }

    // The kind a hit ties by. An episode's strings are folded onto its
    // series, so the hit answers as the series. When the deciding match
    // came off an episode's string, the hit ties as an episode, which
    // keeps the plan's order of series before episodes.
    fn ties(&self, found: &Found, item: usize) -> Kind {
        if found.episode[item] {
            return Kind::Episode;
        }
        match self.items.get(item) {
            Some(held) => held.kind,
            None => Kind::Person,
        }
    }

    // Titles and people share one numbering: a person's number is their
    // position in `people` plus the count of titles.
    fn count(&self) -> usize {
        self.items.len() + self.people.len()
    }

    fn released(&self, item: usize) -> &str {
        match self.items.get(item) {
            Some(held) => self.slice(held.released),
            None => "",
        }
    }

    fn sort_key(&self, item: usize) -> &str {
        match self.items.get(item) {
            Some(held) => self.slice(held.sort_key),
            None => self.slice(self.people[item - self.items.len()].name),
        }
    }

    fn slice(&self, span: Span) -> &str {
        &self.text[span.at as usize..(span.at + span.len) as usize]
    }

    // One title as the slot a wall draws.
    fn slot(&self, item: usize) -> Slot {
        let Some(held) = self.items.get(item) else {
            return self.someone(item - self.items.len());
        };
        Slot {
            library: self.libraries[held.library as usize].clone(),
            kind: self.kinds[held.word as usize].clone(),
            id: self.slice(held.id).to_string(),
            title: self.slice(held.title).to_string(),
            released: self.slice(held.released).to_string(),
            art: self.slice(held.art).to_string(),
            duration: held.duration as i64,
            rating: self.slice(held.rating).to_string(),
            tagline: self.slice(held.tagline).to_string(),
            parts: String::new(),
            episode: None,
            new: 0,
            seasons: held.seasons as i64,
        }
    }

    // One person as the slot their page opens from. The id is the path
    // the person's page reads by.
    fn someone(&self, at: usize) -> Slot {
        let someone = self.people[at];
        let path = self.slice(someone.path);
        Slot {
            library: self.libraries[someone.library as usize].clone(),
            kind: PEOPLE.to_string(),
            art: match someone.headshot {
                true => format!("{path}/{HEADSHOT}"),
                false => String::new(),
            },
            id: path.to_string(),
            title: self.slice(someone.name).to_string(),
            ..Slot::default()
        }
    }
}

// The tally one read builds over every item. An item's rank is the
// weakest of the ranks its query words reached. With the best instead,
// "serial 03" would rank "Serial 12" (whose episode is titled "Segment
// 03") level with "Serial 03", because both match "serial" on the title.
struct Found {
    best: Vec<u8>,
    episode: Vec<bool>,
    word: Vec<u8>,
    from: Vec<bool>,
    turn: Vec<u32>,
    hits: Vec<u32>,
}

impl Found {
    fn new(items: usize) -> Self {
        Self {
            best: vec![0; items],
            episode: vec![false; items],
            word: vec![0; items],
            from: vec![false; items],
            turn: vec![u32::MAX; items],
            hits: vec![0; items],
        }
    }

    // One match of one query word on one item. A word counts once per
    // item however many strings it lands in, so an item with a long
    // plot does not out-hit an item whose title is the word.
    fn mark(&mut self, item: usize, turn: u32, rank: u8, stored: u8) {
        let episode = rank::from_episode(stored);
        if self.turn[item] != turn {
            self.close(item);
            self.turn[item] = turn;
            self.hits[item] += 1;
            self.word[item] = rank;
            self.from[item] = episode;
            return;
        }
        if rank < self.word[item] {
            self.word[item] = rank;
            self.from[item] = episode;
        } else if rank == self.word[item] && !episode {
            self.from[item] = false;
        }
    }

    // Fold the word just finished into the item's rank. The word with the
    // weakest rank decides, and whether that word landed on an episode's
    // string decides the kind the tie breaks by.
    fn close(&mut self, item: usize) {
        if self.word[item] > self.best[item] {
            self.best[item] = self.word[item];
            self.episode[item] = self.from[item];
        }
    }
}

#[cfg(test)]
mod tests;
