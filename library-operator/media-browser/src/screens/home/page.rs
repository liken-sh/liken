// The home page as one read. Every row of the page comes off the source
// here and nowhere else, so the read runs on a thread of its own and the
// page it answers is applied to the screen on a later frame.

use super::banner::{Banner, Title};
use super::{Block, Row, Strip, rows};
use crate::catalog::draw::{self, Date};
use crate::catalog::{Query, Source};
use crate::screens::Item;

/// Every row of the home page as one read answered them, with the date
/// the draw was seeded by.
#[derive(Debug)]
pub struct Page {
    /// The date the day's draw was seeded by.
    pub date: Date,
    /// The rows in the page's order, each one read, with no focus in any
    /// of them.
    pub blocks: Vec<Block>,
}

/// Read every row of the home page on this date: the pool and the day's
/// draw, then each strip in the page's order, then the banner off the
/// strips. It touches no screen, so it runs wherever the caller puts
/// it.
pub fn read(source: &mut dyn Source, today: Date) -> Page {
    let seconds = today.seconds();
    let mut blocks: Vec<Block> = rows(seconds, draw::draw(today, &source.pool()))
        .into_iter()
        .map(Block::new)
        .collect();
    // The released strip's items are in hand while the added strip reads,
    // because the added strip drops what the released strip shows, and the
    // released strip stands before it.
    for index in 0..blocks.len() {
        let released = released(&blocks);
        if let Block::Strip(strip) = &mut blocks[index] {
            strip.reread(source, seconds, &released);
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
        blocks,
    }
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
fn titles(blocks: &[Block], source: &mut dyn Source) -> Vec<Title> {
    let strips: Vec<&Strip> = blocks.iter().filter_map(Block::strip).collect();
    let (recency, drawn): (Vec<&Strip>, Vec<&Strip>) = strips
        .into_iter()
        .filter(|strip| !matches!(strip.row, Row::Libraries | Row::Genres | Row::Franchises))
        .partition(|strip| strip.row.recency());
    Banner::read(drawn.into_iter().chain(recency), source)
}
