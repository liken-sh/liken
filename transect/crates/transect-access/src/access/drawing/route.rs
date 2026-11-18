//! Paths for the lines of the access drawings.  A line leaves a box's
//! right edge, runs horizontally to a vertical lane, up or down the
//! lane, and horizontally into the next box's left edge.

use transect_map::map::section_drawing::Point;

/// The radius, in SVG units, of a rounded turn.
const CORNER: i32 = 8;

/// An SVG path through `points` with rounded turns that ends exactly on
/// the last point.  It is for a line without an arrowhead, such as a
/// caller's line that joins a line shared with other callers.
pub fn open(points: &[Point]) -> String {
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
    let last = points[points.len() - 1];
    d.push_str(&format!(" L {} {}", last.x, last.y));
    d
}

fn step(a: Point, b: Point) -> (i32, i32) {
    ((b.x - a.x).signum(), (b.y - a.y).signum())
}

fn length(a: Point, b: Point) -> i32 {
    (b.x - a.x).abs() + (b.y - a.y).abs()
}

/// The points of a line from `from` horizontally to the x position
/// `lane`, vertically to `to`'s height, and horizontally to `to`.  When
/// both ends are at the same height, the line is straight.
pub fn across(from: Point, lane: i32, to: Point) -> Vec<Point> {
    if from.y == to.y {
        return vec![from, to];
    }
    vec![
        from,
        Point { x: lane, y: from.y },
        Point { x: lane, y: to.y },
        to,
    ]
}

/// The x position of the lane for each line, where `froms[i]` and
/// `tos[i]` are the heights that line `i` leaves and arrives at.  The
/// lanes start at `right`, where the gap before the next boxes ends,
/// and are `gap` apart.  A level line uses `right`.  Each line that
/// runs down takes the lane left of the previous line that runs down,
/// and the lines that run up do the same in reverse order.  So when
/// the lines are sorted by `froms` and their `tos` are in the same
/// order, no two lines cross.
pub fn lanes(froms: &[i32], tos: &[i32], right: i32, gap: i32) -> Vec<i32> {
    let mut lanes = vec![right; froms.len()];
    let mut down = 0;
    for (index, (from, to)) in froms.iter().zip(tos).enumerate() {
        if from < to {
            lanes[index] = right - gap * down;
            down += 1;
        }
    }
    let mut up = 0;
    for (index, (from, to)) in froms.iter().zip(tos).enumerate().rev() {
        if from > to {
            lanes[index] = right - gap * up;
            up += 1;
        }
    }
    lanes
}

/// `count` points spread evenly between `top` and `bottom`.
pub fn spread(count: usize, top: i32, bottom: i32) -> Vec<i32> {
    let step = (bottom - top) / (count as i32 + 1);
    (1..=count as i32).map(|at| top + step * at).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::all_down(vec![0, 10, 20], vec![50, 60, 70], vec![100, 90, 80])]
    #[case::all_up(vec![50, 60, 70], vec![0, 10, 20], vec![80, 90, 100])]
    #[case::down_then_up(vec![0, 100], vec![50, 60], vec![100, 100])]
    #[case::level(vec![10], vec![10], vec![100])]
    fn nests_lanes_so_lines_do_not_cross(
        #[case] froms: Vec<i32>,
        #[case] tos: Vec<i32>,
        #[case] expected: Vec<i32>,
    ) {
        assert_eq!(lanes(&froms, &tos, 100, 10), expected);
    }

    #[rstest]
    fn an_open_line_ends_on_its_last_point() {
        let points = [
            Point { x: 0, y: 0 },
            Point { x: 50, y: 0 },
            Point { x: 50, y: 40 },
        ];
        assert!(open(&points).ends_with("L 50 40"));
    }
}
