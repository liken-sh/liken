// The More from strip of a title's page: the other titles of one studio
// the title names, newest release first, and a last slot that opens the
// studio's whole wall. It takes the place of that studio's name on the
// production line, and the studios that draw no strip stay names there.
//
// A studio earns the strip by the rule the home page applies to a strip
// it draws from its pool. It must hold at least `FLOOR` titles that the
// page does not already show: the title the page is about and the
// members of its set strip and its franchise strips do not count. The
// strip draws the first `SHOWN` of them, as a home strip does, and ends
// in "See all". The wall always holds more than the strip, because it
// holds the title the page is about. The home page draws a candidate by
// weight on a seed of the date, so its strips change from day to day. A
// page draws one strip and must draw the same one at every open, so of
// the studios that earn a strip, the first that the body names takes it.
// That is the studio the production line names first.

use std::collections::HashSet;

use crate::catalog::recency::{FLOOR, SHOWN};
use crate::catalog::{Query, Source, Studio, Work};
use crate::screens::{Item, Step, fitted_strip, slots};
use crate::views::strip;

/// The words before the studio's name on the strip's heading.
pub const HEADING: &str = "More from";

/// The strip: the studio it is about, its heading, and the titles it
/// draws before the slot that opens the studio's wall.
#[derive(Debug)]
pub struct More {
    /// The studio's name as the body spells it, which its wall reads by.
    pub studio: String,
    pub heading: String,
    pub members: Vec<Item>,
}

impl More {
    /// The strip of the first of these studios that holds at least
    /// `FLOOR` titles outside `shown`, or nothing where none does.
    pub fn of(studios: Vec<Studio>, shown: &HashSet<Work>) -> Option<Self> {
        studios.into_iter().find_map(|studio| {
            let query = Query::studio(&studio.name);
            let mut members: Vec<Item> = studio
                .titles
                .into_iter()
                .filter(|slot| !shown.contains(&(slot.library.clone(), slot.id.clone())))
                .map(|slot| Item::of(&query, slot))
                .collect();
            if members.len() < FLOOR {
                return None;
            }
            members.truncate(SHOWN);
            fitted_strip(&mut members);
            Some(Self {
                heading: format!("{HEADING} {}", studio.name),
                studio: studio.name,
                members,
            })
        })
    }

    /// The slots a press reaches: the titles, and the slot after them.
    pub fn count(&self) -> usize {
        self.members.len() + 1
    }

    /// The slot that ends the strip: the words a home strip ends in, with
    /// no art.
    pub fn last(&self) -> strip::Last<'static> {
        strip::Last {
            words: strip::SEE_ALL,
            library: "",
            art: "",
        }
    }

    /// What a select on one slot opens: a title's page over this one, or
    /// the studio's wall from the last slot.
    pub fn select(&self, index: usize, source: &mut dyn Source) -> Step {
        match self.members.get(index) {
            Some(item) => slots::opened(item, source),
            None if index == self.members.len() => {
                slots::see_all(&Query::studio(&self.studio), source)
            }
            None => Step::Stay,
        }
    }
}

/// The studios the production line names: every studio the body names,
/// except the one the strip is about.
pub fn named(studios: &[String], more: Option<&More>) -> Vec<String> {
    studios
        .iter()
        .filter(|name| more.is_none_or(|more| more.studio != **name))
        .cloned()
        .collect()
}

#[cfg(test)]
mod tests;
