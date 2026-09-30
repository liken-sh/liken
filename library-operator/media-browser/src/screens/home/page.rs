// The home page as one read. Every row of the page comes off the source
// here and nowhere else, so the read runs on a thread of its own and the
// page it answers is applied to the screen on a later frame.

use super::banner::{Banner, Title};
use super::{Block, Row, Strip, rows};
use crate::audience::Viewer;
use crate::catalog::draw::{self, Date};
use crate::catalog::{Query, Source};
use crate::screens::Item;

/// Every row of the home page as one read answered them, with the date
/// the draw was seeded by.
#[derive(Debug)]
pub struct Page {
    /// The date the day's draw was seeded by.
    pub date: Date,
    /// The people the continue-watching row was read for. The screen keeps
    /// them so a re-read of its own asks for the same audience.
    pub people: Vec<String>,
    // Those people as the row's heading draws them, one per person in the
    // same order.
    pub viewers: Vec<Viewer>,
    /// The rows in the page's order, each one read, with no focus in any
    /// of them.
    pub blocks: Vec<Block>,
}

// The scope ends through `Drop`, including when one of its reads panics.
struct PageRead<'a> {
    source: &'a mut dyn Source,
}

impl<'a> PageRead<'a> {
    fn begin(source: &'a mut dyn Source) -> Self {
        source.begin_page_read();
        Self { source }
    }
}

impl Drop for PageRead<'_> {
    fn drop(&mut self) {
        self.source.end_page_read();
    }
}

/// Read every row of the home page on this date: the pool and the day's
/// draw, then each strip in the page's order, then the banner off the
/// strips. It touches no screen, so it runs wherever the caller puts
/// it.
///
/// Read every row of the page. `people` is the audience the
/// continue-watching row is read for.
// `viewers` are those people as the row's heading draws them. An empty audience
// takes no continue-watching row at all, neither a heading nor cards, and
// the row is back with the next answer.
pub fn read(source: &mut dyn Source, today: Date, people: &[String], viewers: &[Viewer]) -> Page {
    let scope = PageRead::begin(source);
    let source = &mut *scope.source;
    let seconds = today.seconds();
    let mut blocks: Vec<Block> = rows(seconds, draw::draw(today, &source.pool()))
        .into_iter()
        .filter(|row| !(people.is_empty() && *row == Row::Continue))
        .map(Block::new)
        .collect();
    // The released strip's items are in hand while the added strip reads,
    // because the added strip drops what the released strip shows, and the
    // released strip stands before it.
    for index in 0..blocks.len() {
        let released = released(&blocks);
        if let Block::Strip(strip) = &mut blocks[index] {
            strip.reread(source, seconds, &released, people, viewers);
        }
    }
    let titles = titles(&blocks, source);
    if let Some(Block::Banner(banner)) = blocks
        .iter_mut()
        .find(|block| matches!(block, Block::Banner(_)))
    {
        banner.reread(titles);
    }
    Page {
        date: today,
        people: people.to_vec(),
        viewers: viewers.to_vec(),
        blocks,
    }
}

/// Read the continue-watching row alone, for a change the progress store
/// made that can add, move, or remove a card. Every other row of the page
/// stays as it was read, because no progress change moves them.
pub fn read_row(source: &mut dyn Source, people: &[String], viewers: &[Viewer]) -> Strip {
    let mut strip = Strip::new(Row::Continue);
    strip.reread(source, 0, &[], people, viewers);
    strip
}

// The items of the released strip, or nothing until it is read.
fn released(blocks: &[Block]) -> Vec<Item> {
    blocks
        .iter()
        .filter_map(Block::strip)
        .find(|strip| matches!(strip.row, Row::Query(Query::Released { .. })))
        .map(|strip| strip.items.clone())
        .unwrap_or_default()
}

// The banner's titles from the drawn strips, then the two recency
// strips. The banner is read after the strips because it holds one title
// from each of them.
// The continue-watching row feeds the banner nothing, because a work the
// audience already started is not one to introduce.
fn titles(blocks: &[Block], source: &mut dyn Source) -> Vec<Title> {
    let strips: Vec<&Strip> = blocks.iter().filter_map(Block::strip).collect();
    let (recency, drawn): (Vec<&Strip>, Vec<&Strip>) = strips
        .into_iter()
        .filter(|strip| {
            !matches!(
                strip.row,
                Row::Continue | Row::Libraries | Row::Genres | Row::Franchises
            )
        })
        .partition(|strip| strip.row.recency());
    Banner::read(drawn.into_iter().chain(recency), source)
}
