//! The "who may change it" drawing.  The accounts that may change the
//! workload are on the left, the GitOps objects that apply it
//! (appliers) are in the middle, and the workload is on the right.
//! Each line has a label that says what the account may do.  Lines into
//! the workload enter its left edge at points spread along it, in the
//! order of the boxes that they leave, so the lines do not cross.  The
//! accounts that may patch an applier are grouped together and level
//! with the applier, so a line up or down to an applier crosses no line
//! that continues to the workload.

use super::route::{across, lanes, spread};
use super::{Drawing, LABEL, PLAIN, height};
use transect_map::map::section_drawing::{Point, Rect, SectionBox, Style};

const LEFT_X: i32 = 20;
const LEFT_W: i32 = 350;
const APPLIER_X: i32 = 600;
const APPLIER_W: i32 = 230;
const WORKLOAD_X: i32 = 930;
const WORKLOAD_W: i32 = 240;
const TOP: i32 = 20;
const GAP: i32 = 12;
/// The vertical distance between two lines that leave one account's
/// box.
const LINE_GAP: i32 = 52;

/// The boxes that [`draw`] lays out: the accounts that may change the
/// workload, the appliers, and the workload.
#[derive(Debug)]
pub struct Change {
    pub changers: Vec<Changer>,
    pub appliers: Vec<SectionBox>,
    pub workload: SectionBox,
}

/// A box for one account, or for several that share the same lines,
/// with its style class and each line that leaves it, as a target and a
/// label.
#[derive(Debug)]
pub struct Changer {
    pub boxed: SectionBox,
    pub class: &'static str,
    pub lines: Vec<(Target, String)>,
}

/// Where a line goes: to the workload, or to one of the appliers.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Target {
    Workload,
    Applier(usize),
}

pub fn draw(mut change: Change) -> Drawing {
    let mut drawing = Drawing::new();
    in_applier_order(&mut change.changers);
    let mut y = TOP;
    // `into` collects the lines that end at the workload, as the point
    // where each starts and its label. `to_appliers` collects the lines
    // from an account to an applier, with the applier's index.
    let mut into: Vec<(Point, String)> = Vec::new();
    let mut to_appliers: Vec<(Point, usize, String)> = Vec::new();
    for changer in change.changers {
        let count = changer.lines.len().max(1) as i32;
        let h = height(&changer.boxed, LEFT_W).max(24 + LINE_GAP * (count - 1) + 28);
        let rect = Rect {
            x: LEFT_X,
            y,
            width: LEFT_W,
            height: h,
        };
        let first = match count {
            1 => rect.middle(),
            _ => y + 32,
        };
        for (at, (target, label)) in changer.lines.into_iter().enumerate() {
            let from = Point {
                x: rect.right(),
                y: first + LINE_GAP * at as i32,
            };
            match target {
                Target::Workload => into.push((from, label)),
                Target::Applier(index) => to_appliers.push((from, index, label)),
            }
        }
        drawing.place(changer.boxed, changer.class, rect);
        y += h + GAP;
    }
    // An applier is level with the first account that may patch it.  An
    // applier that no account may patch goes below the accounts, so the
    // lines that pass it on the way to the workload do not cross it.
    let mut applier_bottom = TOP;
    let below = y;
    for (index, boxed) in change.appliers.into_iter().enumerate() {
        let wanted = to_appliers.iter().find(|(_, at, _)| *at == index);
        let h = height(&boxed, APPLIER_W);
        // A line to the workload that leaves above the first line into
        // the applier passes above the applier's box, so the applier
        // starts below that line.
        let over = wanted.and_then(|(first, _, _)| {
            let above = into.iter().map(|(from, _)| from.y).filter(|y| *y < first.y);
            above.max().map(|y| y + 14)
        });
        let top = wanted
            .map_or(below, |(from, _, _)| from.y - h / 2)
            .max(applier_bottom)
            .max(over.unwrap_or(TOP));
        let rect = drawing.place(
            boxed,
            PLAIN,
            Rect {
                x: APPLIER_X,
                y: top,
                width: APPLIER_W,
                height: h,
            },
        );
        applier_bottom = rect.bottom() + GAP;
        for (from, _, label) in to_appliers.iter().filter(|(_, at, _)| *at == index) {
            let to = Point {
                x: APPLIER_X,
                y: rect.middle(),
            };
            drawing.line(&across(*from, APPLIER_X - 24, to), Style::Solid, true, None);
            drawing.text(from.x + 12, from.y - 12, label, LABEL);
        }
        into.push((
            Point {
                x: rect.right(),
                y: rect.middle(),
            },
            "applies".into(),
        ));
    }
    into.sort_by_key(|(from, _)| from.y);
    let left_bottom = y.max(applier_bottom);
    let h = (36 * (into.len() as i32 + 1))
        .max(height(&change.workload, WORKLOAD_W))
        .max(140);
    let top = (TOP + (left_bottom - TOP - h) / 2).max(TOP);
    let rect = drawing.place(
        change.workload,
        "section-box section-box-workload",
        Rect {
            x: WORKLOAD_X,
            y: top,
            width: WORKLOAD_W,
            height: h,
        },
    );
    let entries = spread(into.len(), rect.y, rect.bottom());
    let froms: Vec<i32> = into.iter().map(|(from, _)| from.y).collect();
    let lanes = lanes(&froms, &entries, WORKLOAD_X - 40, 18);
    for (((from, label), entry), lane) in into.into_iter().zip(entries).zip(lanes) {
        let to = Point {
            x: WORKLOAD_X,
            y: entry,
        };
        drawing.line(&across(from, lane, to), Style::Solid, true, None);
        drawing.text(from.x + 12, from.y - 12, &label, LABEL);
    }
    drawing.height = drawing.bottom() + 20;
    drawing
}

/// Sorts `changers` so that the accounts that may patch an applier come
/// first, in one run for each applier, in the appliers' order, and the
/// accounts that patch no applier come last.  In a run, the first
/// account that also has a line to the workload moves to the top and
/// draws that line above its line into the applier, so the line passes
/// over the applier.  Every other such account comes after the accounts
/// that only patch the applier and draws its line below, so the line
/// passes under the applier.  When a run has more than two such
/// accounts, the line to the workload of each one between the first and
/// the last crosses a line into the applier.  The sorts are stable, so
/// accounts that compare equal keep their order.
fn in_applier_order(changers: &mut Vec<Changer>) {
    for changer in changers.iter_mut() {
        changer.lines.sort_by_key(|(target, _)| match target {
            Target::Applier(index) => (0, *index),
            Target::Workload => (1, 0),
        });
    }
    changers.sort_by_key(|changer| {
        let applier = changer.lines.iter().find_map(|(target, _)| match target {
            Target::Applier(index) => Some(*index),
            Target::Workload => None,
        });
        let direct = changer
            .lines
            .iter()
            .any(|(target, _)| *target == Target::Workload);
        (applier.unwrap_or(usize::MAX), direct)
    });
    let applier_of = |changer: &Changer| match changer.lines.first() {
        Some((Target::Applier(index), _)) => Some(*index),
        _ => None,
    };
    let mut start = 0;
    while start < changers.len() {
        let applier = applier_of(&changers[start]);
        let end = (start..changers.len())
            .find(|at| applier_of(&changers[*at]) != applier)
            .unwrap_or(changers.len());
        let both = (start..end).find(|at| {
            let lines = &changers[*at].lines;
            lines.iter().any(|(target, _)| *target == Target::Workload)
        });
        if let (Some(_), Some(both)) = (applier, both) {
            let mut first = changers.remove(both);
            first
                .lines
                .sort_by_key(|(target, _)| *target != Target::Workload);
            changers.insert(start, first);
        }
        start = end;
    }
}
