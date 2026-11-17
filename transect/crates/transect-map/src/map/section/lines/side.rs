//! The lines between a side box and the spine.  Each runs across from
//! the side box's inner edge at its middle: straight into a box level
//! with it, and otherwise across to a lane in the gap beside the spine,
//! up or down the lane, and across into the box.  A box with another
//! between it and the side column, as a machine in a fan is, takes its
//! line from below instead, so the line never crosses a box.
//!
//! Lines of one style and label into one edge of one box share a lane
//! and an entry, as a fan shares a trunk.  Each other line has its own
//! lane, and the one whose side box is farthest from its target takes
//! the lane nearest the spine, so lines that share a gap nest without
//! crossing.  The lines into one edge of one box enter at points spread
//! along it, in the order of their side boxes.

use std::collections::BTreeMap;

use super::{ARROW, Anchor, CORNER, Drawn, Label, Line, Link, Point};
use crate::map::section::layout::{At, Column, Layout, Rect, Side};

/// Room between two lanes in one gap.
const LANE: i32 = 8;
/// How far below a box the lines that enter it from below turn.
const UNDER: i32 = 14;
/// How far above or below a side line its label is drawn, and how far
/// from its side box.
const RISE: i32 = 11;
const LABEL_GAP: i32 = 8;

/// One side line before its points are known.
struct Plan<'a> {
    link: &'a Link,
    side: Side,
    source: Rect,
    target: Rect,
    target_at: At,
    /// Whether the line enters the target from below.
    under: bool,
    /// The target's end of the line.
    entry: Point,
    /// The gap between the side column and the spine boxes the line
    /// passes, left edge first.
    gap: (i32, i32),
}

pub fn route(layout: &Layout, links: &[&Link]) -> Drawn {
    let mut plans: Vec<Plan> = links.iter().filter_map(|l| plan(layout, l)).collect();
    spread_entries(&mut plans);
    let lanes = lanes(&plans);
    let labelled = labelled(&plans);
    let mut drawn = Drawn::default();
    for (index, (plan, lane)) in plans.iter().zip(lanes).enumerate() {
        let start = Point {
            x: inner_edge(plan.side, &plan.source),
            y: plan.source.middle(),
        };
        let mut points = vec![start];
        if plan.under {
            let under = plan.target.bottom() + UNDER;
            points.extend([
                Point {
                    x: lane,
                    y: start.y,
                },
                Point { x: lane, y: under },
                Point {
                    x: plan.entry.x,
                    y: under,
                },
            ]);
        } else if plan.entry.y != start.y {
            points.extend([
                Point {
                    x: lane,
                    y: start.y,
                },
                Point {
                    x: lane,
                    y: plan.entry.y,
                },
            ]);
        }
        points.push(plan.entry);
        if plan.link.from == plan.target_at {
            points.reverse();
        }
        if labelled.contains(&index) {
            drawn.labels.push(label(plan, start));
        }
        drawn.lines.push(Line {
            from: points[0],
            to: points[points.len() - 1],
            d: rounded(&points),
            style: plan.link.style,
            title: plan.link.title.clone(),
        });
    }
    drawn
}

fn plan<'a>(layout: &Layout, link: &'a Link) -> Option<Plan<'a>> {
    let (side_at, target_at, side) = match (link.from.on_side(), link.to.on_side()) {
        (Some(side), None) => (link.from, link.to, side),
        (None, Some(side)) => (link.to, link.from, side),
        _ => return None,
    };
    let source = layout.rect(side_at)?;
    let target = layout.rect(target_at)?;
    let Column::Tier(tier) = target_at.column else {
        return None;
    };
    let count = layout.rows[target_at.row].boxes[tier].len();
    let outermost = match side {
        Side::Left => target_at.index == 0,
        Side::Right => target_at.index + 1 == count,
    };
    let entry = match (outermost, side) {
        (true, _) => Point {
            x: inner_edge(opposite(side), &target),
            y: target.middle(),
        },
        (false, Side::Left) => Point {
            x: target.x + target.width / 4,
            y: target.bottom(),
        },
        (false, Side::Right) => Point {
            x: target.right() - target.width / 4,
            y: target.bottom(),
        },
    };
    let rows = side_at.row.min(target_at.row)..=side_at.row.max(target_at.row);
    let spine = rows.flat_map(|row| layout.rows[row].boxes.iter().flatten());
    let gap = match side {
        Side::Left => (source.right(), spine.map(|r| r.x).min().unwrap_or(target.x)),
        Side::Right => (
            spine.map(Rect::right).max().unwrap_or(target.right()),
            source.x,
        ),
    };
    Some(Plan {
        link,
        side,
        source,
        target,
        target_at,
        under: !outermost,
        entry,
        gap,
    })
}

/// Where a line turns toward its target: at its entry, or for a line
/// that enters from below, under the target.
fn turn(plan: &Plan) -> i32 {
    match plan.under {
        true => plan.target.bottom() + UNDER,
        false => plan.entry.y,
    }
}

/// The lines that draw their bundle's label, one line per bundle, as a
/// fan draws one label: the topmost when its lane runs down from it, and
/// otherwise the bottommost, whose lane runs up.  The lane runs past
/// every other line of the bundle, through the place where that line's
/// label would be.
fn labelled(plans: &[Plan]) -> Vec<usize> {
    let middle = |i: &&usize| plans[**i].source.middle();
    bundles(plans, |_| true)
        .into_iter()
        .filter_map(|bundle| {
            let top = *bundle.iter().min_by_key(middle)?;
            match plans[top].source.middle() <= turn(&plans[top]) {
                true => Some(top),
                false => bundle.iter().max_by_key(middle).copied(),
            }
        })
        .collect()
}

/// A line's label beside its side box, on the side of the line its lane
/// does not turn toward, so the lane never runs through it.
fn label(plan: &Plan, start: Point) -> Label {
    let (x, anchor) = match plan.side {
        Side::Left => (start.x + LABEL_GAP, Anchor::Start),
        Side::Right => (start.x - LABEL_GAP, Anchor::End),
    };
    let y = match turn(plan) < start.y {
        true => start.y + RISE,
        false => start.y - RISE,
    };
    Label {
        x,
        y,
        text: plan.link.label.clone(),
        anchor,
    }
}

fn opposite(side: Side) -> Side {
    match side {
        Side::Left => Side::Right,
        Side::Right => Side::Left,
    }
}

/// The edge of `rect` that faces the spine from `side`.
fn inner_edge(side: Side, rect: &Rect) -> i32 {
    match side {
        Side::Left => rect.right(),
        Side::Right => rect.x,
    }
}

/// The lines that share a lane and an entry: those of one style and
/// label into one edge of one box, as several callers of a workload
/// are.  They join as a fan does, so ten callers draw one trunk rather
/// than ten lanes.
fn bundles(plans: &[Plan], on: impl Fn(&Plan) -> bool) -> Vec<Vec<usize>> {
    let mut bundles: BTreeMap<(At, Side, bool, String, String), Vec<usize>> = BTreeMap::new();
    for (index, plan) in plans.iter().enumerate().filter(|(_, p)| on(p)) {
        let style = format!("{:?}", plan.link.style);
        let key = (
            plan.target_at,
            plan.side,
            plan.under,
            style,
            plan.link.label.clone(),
        );
        bundles.entry(key).or_default().push(index);
    }
    bundles.into_values().collect()
}

/// Spreads the bundles into each edge of each box along it, in the
/// order of their side boxes top to bottom.  A lone line into a box
/// level with its side box enters at the side box's middle, so it runs
/// straight.
fn spread_entries(plans: &mut [Plan]) {
    let mut edges: BTreeMap<(At, Side), Vec<Vec<usize>>> = BTreeMap::new();
    for bundle in bundles(plans, |p| !p.under) {
        let first = &plans[bundle[0]];
        edges
            .entry((first.target_at, first.side))
            .or_default()
            .push(bundle);
    }
    for mut edge in edges.into_values() {
        let top = |bundle: &Vec<usize>| bundle.iter().map(|&i| plans[i].source.middle()).min();
        edge.sort_by_key(top);
        if let [bundle] = &edge[..]
            && let [only] = bundle[..]
        {
            let plan = &mut plans[only];
            let level = plan.source.middle();
            if (plan.target.y + CORNER..=plan.target.bottom() - CORNER).contains(&level) {
                plan.entry.y = level;
            }
            continue;
        }
        let n = i32::try_from(edge.len()).unwrap_or(i32::MAX);
        for (k, bundle) in (1..).zip(&edge) {
            for &i in bundle {
                let target = plans[i].target;
                plans[i].entry.y = target.y + target.height * k / (n + 1);
            }
        }
    }
}

/// Each line's lane, in the gap it passes, one lane per bundle.  The
/// bundles on one side are ordered by how far their farthest side box
/// is from where they turn, the farthest nearest the spine.
fn lanes(plans: &[Plan]) -> Vec<i32> {
    let mut lanes = vec![0; plans.len()];
    let reach = |i: usize| (plans[i].source.middle() - turn(&plans[i])).abs();
    for side in [Side::Left, Side::Right] {
        let mut on = bundles(plans, |p| p.side == side);
        on.sort_by_key(|bundle| std::cmp::Reverse(bundle.iter().map(|&i| reach(i)).max()));
        let m = i32::try_from(on.len()).unwrap_or(i32::MAX);
        for (k, bundle) in (0..).zip(&on) {
            let low = bundle.iter().map(|&i| plans[i].gap.0).max().unwrap_or(0);
            let high = bundle.iter().map(|&i| plans[i].gap.1).min().unwrap_or(0);
            let offset = k * LANE - (m - 1) * LANE / 2;
            let toward = match side {
                Side::Left => -offset,
                Side::Right => offset,
            };
            let lane = (low + high) / 2 + toward;
            let lane = lane.clamp(low + LANE / 2, (high - LANE / 2).max(low + LANE / 2));
            for &i in bundle {
                lanes[i] = lane;
            }
        }
    }
    lanes
}

/// A path through `points`, each turn rounded, that stops `ARROW` short
/// of the last point so that the arrow's tip touches it.  Every segment
/// is horizontal or vertical.
pub fn rounded(points: &[Point]) -> String {
    let mut points: Vec<Point> = points.to_vec();
    points.dedup();
    let mut d = format!("M {} {}", points[0].x, points[0].y);
    for window in points.windows(3) {
        let [a, b, c] = [window[0], window[1], window[2]];
        let (into, out) = (step(a, b), step(b, c));
        if into == out {
            continue;
        }
        let r = CORNER.min(length(a, b) / 2).min(length(b, c) / 2);
        let (x0, y0) = (b.x - into.0 * r, b.y - into.1 * r);
        let (x1, y1) = (b.x + out.0 * r, b.y + out.1 * r);
        d.push_str(&format!(" L {x0} {y0} Q {} {} {x1} {y1}", b.x, b.y));
    }
    let n = points.len();
    let (last, before) = (points[n - 1], points[n.saturating_sub(2)]);
    let (dx, dy) = step(before, last);
    let shortened = ARROW.min(length(before, last));
    let (x, y) = (last.x - dx * shortened, last.y - dy * shortened);
    d.push_str(&format!(" L {x} {y}"));
    d
}

fn step(a: Point, b: Point) -> (i32, i32) {
    ((b.x - a.x).signum(), (b.y - a.y).signum())
}

fn length(a: Point, b: Point) -> i32 {
    (b.x - a.x).abs() + (b.y - a.y).abs()
}

#[cfg(test)]
mod tests;
