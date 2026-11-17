//! The fixed layout of a known kind's diagram: the object in the middle
//! of a three by three grid and one box per relation in the eight places
//! around it, in the order its knowledge lists them, from the top left
//! across and down.
//! A relation past the eighth takes a row of its own below, with no
//! line, since a line to it would cross the box beneath the object.
//! Each line runs straight from the object's edge, or its corner for a
//! diagonal, to the facing edge or corner of the relation's box, so
//! the lines fan out like the arrows in the boxes' labels.  A row is as
//! tall as its tallest box needs, so no box clips its text.

use crate::map::section::lines::{ARROW, Point};

/// A box's width, height, and the gaps between boxes, in pixels.
const WIDTH: i32 = 290;
const HEIGHT: i32 = 150;
const GAP_X: i32 = 64;
const GAP_Y: i32 = 48;

/// The places around the object, as columns and rows of the grid from
/// its top left, with the arrow each box's label shows.
const AROUND: [(i32, i32, &str); 8] = [
    (0, 0, "↖"),
    (1, 0, "↑"),
    (2, 0, "↗"),
    (0, 1, "←"),
    (2, 1, "→"),
    (0, 2, "↙"),
    (1, 2, "↓"),
    (2, 2, "↘"),
];

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Rect {
    pub x: i32,
    pub y: i32,
    pub width: i32,
    pub height: i32,
}

/// The place of one relation's box, and the line to it when it has one.
#[derive(Debug, PartialEq, Eq)]
pub struct Place {
    pub rect: Rect,
    pub arrow: &'static str,
    /// The line's path from the object to the box, stopped short so the
    /// arrowhead's tip touches the box.
    pub line: Option<String>,
}

/// The board's size, the object's box, and the places of the relations
/// around it, given the height each relation's box needs and the
/// object's box needs.  Every box in a row takes the row's height, the
/// tallest any of them needs and never less than `HEIGHT`, so a box
/// grows to fit its lines and the rows below move down to make room.
pub fn places(needs: &[i32], center_needs: i32) -> (i32, i32, Rect, Vec<Place>) {
    let at: Vec<(i32, i32, &str)> = (0..needs.len())
        .map(|index| match AROUND.get(index) {
            Some(&place) => place,
            None => {
                let extra = (index - AROUND.len()) as i32;
                (extra % 3, 3 + extra / 3, "")
            }
        })
        .collect();
    let rows = at
        .iter()
        .map(|&(_, row, _)| row + 1)
        .max()
        .unwrap_or(0)
        .max(2);
    let mut heights = vec![HEIGHT; rows as usize];
    for (&(_, row, _), &need) in at.iter().zip(needs) {
        heights[row as usize] = heights[row as usize].max(need);
    }
    heights[1] = heights[1].max(center_needs);
    let cell = |column: i32, row: i32| Rect {
        x: column * (WIDTH + GAP_X),
        y: heights[..row as usize].iter().map(|h| h + GAP_Y).sum(),
        width: WIDTH,
        height: heights[row as usize],
    };
    let center = cell(1, 1);
    let places = at
        .iter()
        .map(|&(column, row, arrow)| {
            let rect = cell(column, row);
            Place {
                rect,
                arrow,
                line: (row < 3).then(|| line(center, rect, column - 1, row - 1)),
            }
        })
        .collect();
    let last = cell(0, rows - 1);
    (3 * WIDTH + 2 * GAP_X, last.y + last.height, center, places)
}

/// The straight path from `from`'s side facing `dx` and `dy` to the
/// facing side of `to`.
fn line(from: Rect, to: Rect, dx: i32, dy: i32) -> String {
    let start = Point {
        x: from.x + from.width / 2 + dx * from.width / 2,
        y: from.y + from.height / 2 + dy * from.height / 2,
    };
    let end = Point {
        x: to.x + to.width / 2 - dx * to.width / 2,
        y: to.y + to.height / 2 - dy * to.height / 2,
    };
    let (run, rise) = (f64::from(end.x - start.x), f64::from(end.y - start.y));
    let length = run.hypot(rise);
    let short = |delta: f64| (delta * f64::from(ARROW) / length).round() as i32;
    let tip = Point {
        x: end.x - short(run),
        y: end.y - short(rise),
    };
    format!("M {} {} L {} {}", start.x, start.y, tip.x, tip.y)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::above(1, "M 499 198 L 499 154")]
    #[case::to_the_left(3, "M 354 273 L 294 273")]
    #[case::below_on_the_right(7, "M 644 348 L 705 394")]
    fn draws_each_line_from_the_object_to_its_box(#[case] at: usize, #[case] d: &str) {
        let (_, _, _, places) = places(&[0; 8], 0);
        assert_eq!(places[at].line.as_deref(), Some(d));
    }

    #[rstest]
    fn fills_the_places_around_the_object_in_order() {
        let (_, _, _, places) = places(&[0; 3], 0);
        let arrows: Vec<&str> = places.iter().map(|p| p.arrow).collect();
        assert_eq!(arrows, vec!["↖", "↑", "↗"]);
    }

    #[rstest]
    fn puts_a_ninth_relation_in_a_row_below_with_no_line() {
        let (width, height, _, places) = places(&[0; 9], 0);
        let ninth = &places[8];
        assert_eq!(
            (
                ninth.rect.x,
                ninth.rect.y,
                ninth.line.is_none(),
                width,
                height
            ),
            (
                0,
                3 * (HEIGHT + GAP_Y),
                true,
                3 * WIDTH + 2 * GAP_X,
                4 * HEIGHT + 3 * GAP_Y
            )
        );
    }

    #[rstest]
    fn is_the_grid_when_nothing_is_named() {
        let (_, height, _, places) = places(&[], 0);
        assert_eq!((height, places.len()), (2 * HEIGHT + GAP_Y, 0));
    }

    #[rstest]
    #[case::a_tall_box_above(vec![0, 400, 0, 0], 0, (400, 400 + GAP_Y, HEIGHT))]
    #[case::a_tall_object(vec![0, 0, 0, 0], 300, (HEIGHT, HEIGHT + GAP_Y, 300))]
    #[case::a_tall_box_beside(vec![0, 0, 0, 0, 250], 0, (HEIGHT, HEIGHT + GAP_Y, 250))]
    fn grows_a_row_to_its_tallest_box(
        #[case] needs: Vec<i32>,
        #[case] center_needs: i32,
        #[case] rows: (i32, i32, i32),
    ) {
        let (_, _, center, places) = places(&needs, center_needs);
        assert_eq!((places[0].rect.height, center.y, center.height), rows);
    }

    #[rstest]
    fn ends_each_line_on_its_grown_box() {
        let (_, _, _, places) = places(&[0, 400], 0);
        assert_eq!(places[1].line.as_deref(), Some("M 499 448 L 499 404"));
    }
}
