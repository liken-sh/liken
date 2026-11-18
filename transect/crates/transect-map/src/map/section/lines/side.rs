//! The lines between a side box and the spine boxes.  Each starts at
//! the middle of the side box's inner edge. When the target box is level
//! with it, the line runs straight across. Otherwise it runs across to
//! a lane in the gap beside the spine, up or down the lane, and across
//! into the target. When another box is between the target and the
//! side column, as with a machine in a fan, the line enters the target
//! from below so that it never crosses a box.
//!
//! Lines with the same style and label into the same edge of the same
//! box form a bundle. A bundle shares one lane and one entry point, as
//! a fan shares a trunk. Each bundle has its own lane. The bundle whose
//! side box is farthest above or below the point where it turns into
//! its target gets the lane nearest the spine, so that bundles in one
//! gap nest without crossing. The bundles
//! into one edge of a box enter at points spread along that edge, in
//! the order of their side boxes.

use std::collections::BTreeMap;

use super::{ARROW, Anchor, CORNER, Drawn, Label, Line, Link, Point};
use crate::map::section::layout::{At, Column, Layout, Rect, Side};

/// The distance between two lanes in one gap.
const LANE: i32 = 8;
/// How far below its target a line turns when it enters from below.
const UNDER: i32 = 14;
/// How far above or below its line a label is drawn.
const RISE: i32 = 11;
/// How far from the side box's edge a label is drawn.
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
    /// Where the line meets the target.
    entry: Point,
    /// The x range of the gap between the side column and the spine
    /// boxes that the line passes, as (left edge, right edge).
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

/// The y coordinate where a line turns toward its target: at the entry
/// point, or below the target for a line that enters from below.
fn turn(plan: &Plan) -> i32 {
    match plan.under {
        true => plan.target.bottom() + UNDER,
        false => plan.entry.y,
    }
}

/// The index of the line in each bundle that draws the bundle's label.
/// It is the line where the lane starts: the topmost line when the lane
/// runs down, and otherwise the bottommost. The other lines get no
/// label, because the lane passes their side boxes, where their labels
/// would be drawn.
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

/// A line's label, beside its side box. It goes on the opposite side of
/// the line from the way the lane turns, so the lane never runs through
/// the text.
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

/// Groups the lines that `on` accepts into bundles, so that the callers
/// of one workload draw one trunk instead of a lane each.
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

/// Sets the entry point of each bundle, spreading the bundles into one
/// edge of a box evenly along it, in the order of their side boxes from
/// top to bottom. A single line into a box that is level with its side
/// box enters at the side box's middle, so that it runs straight.
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

/// The x coordinate of each line's lane in the gap it passes.
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

/// An SVG path through `points` with rounded corners. It stops `ARROW`
/// short of the last point so that the arrowhead's tip lands on it. Every
/// segment must be horizontal or vertical.
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
