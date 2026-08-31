//! The mark comes out of `liken.svg`. The values below are the ones that file
//! writes, so an edit to a polygon fails here until the test states the new
//! shape.

use iced_widget::core::{Color, Point};
use liken_iced::mark;
use liken_iced::palette;

/// Where the tests place the mark: the middle of a 1920 by 1080 canvas, at the
/// third of the width `liken`'s idle screen gives it.
const CENTER: Point = Point::new(960.0, 540.0);
const SPAN: f32 = 640.0;

/// A vertex of the mark mapped onto the canvas, computed from [`mark::bounds`]
/// alone. The tests map their own vertices, so a mapping the crate got wrong
/// does not agree with itself.
fn onto_canvas(point: Point) -> Point {
    let box_ = mark::bounds();
    let scale = SPAN / box_.width;

    Point::new(
        CENTER.x + (point.x - box_.center().x) * scale,
        CENTER.y + (point.y - box_.center().y) * scale,
    )
}

fn distance(from: Point, to: Point) -> f32 {
    ((to.x - from.x).powi(2) + (to.y - from.y).powi(2)).sqrt()
}

#[test]
fn the_mark_is_fourteen_hexagons() {
    assert_eq!(mark::hexagons().len(), 14);
}

#[test]
fn every_hexagon_has_six_vertices_and_no_two_are_the_same() {
    for (index, hexagon) in mark::hexagons().iter().enumerate() {
        assert_eq!(hexagon.points.len(), 6, "hexagon {index}");

        for (at, point) in hexagon.points.iter().enumerate() {
            assert!(
                !hexagon.points[at + 1..].contains(point),
                "hexagon {index} repeats a vertex"
            );
        }
    }
}

#[test]
fn the_first_hexagon_is_the_first_polygon_of_the_file() {
    let hexagon = mark::hexagons()[0];

    assert_eq!(
        hexagon.points,
        [
            Point::new(-4.33, -9.27),
            Point::new(2.77, -5.17),
            Point::new(2.77, 3.03),
            Point::new(-4.33, 7.13),
            Point::new(-11.43, 3.03),
            Point::new(-11.43, -5.17),
        ]
    );
    assert_eq!(hexagon.fill, Color::from_rgb8(0x6e, 0x83, 0x52));
    assert_eq!(hexagon.stroke_width, 1.40);
}

#[test]
fn every_stroke_width_is_the_files_own() {
    let widths: Vec<f32> = mark::hexagons().iter().map(|it| it.stroke_width).collect();

    assert_eq!(
        widths,
        [
            1.40, 1.40, 1.40, 1.40, 1.40, 1.40, 1.40, 1.19, 0.98, 0.77, 1.12, 0.91, 1.12, 1.26,
        ]
    );
}

#[test]
fn the_mark_draws_in_four_greens_and_one_orange() {
    let mut fills: Vec<String> = mark::hexagons()
        .iter()
        .map(|it| {
            let byte = |channel: f32| (channel * 255.0).round() as u8;

            format!(
                "#{:02x}{:02x}{:02x}",
                byte(it.fill.r),
                byte(it.fill.g),
                byte(it.fill.b)
            )
        })
        .collect();
    fills.sort();
    fills.dedup();

    assert_eq!(
        fills,
        ["#4a5d3a", "#6e8352", "#93a877", "#b4c49a", "#e0872f"]
    );
    // The darkest green and the palest are the two `--link` tokens of
    // liken.css, one for each color scheme.
    assert_eq!(palette::light().link, Color::from_rgb8(0x4a, 0x5d, 0x3a));
    assert_eq!(palette::dark().link, Color::from_rgb8(0xb4, 0xc4, 0x9a));
}

#[test]
fn the_box_holds_every_vertex() {
    let box_ = mark::bounds();

    assert_eq!(
        (box_.x, box_.y, box_.width, box_.height),
        (-35.99, -36.81, 64.03, 72.3)
    );

    let vertices: Vec<Point> = mark::hexagons().iter().flat_map(|it| it.points).collect();
    let extreme = |of: fn(&Point) -> f32, fold: fn(f32, f32) -> f32, from: f32| {
        vertices.iter().map(of).fold(from, fold)
    };

    let left = extreme(|it| it.x, f32::min, f32::MAX);
    let top = extreme(|it| it.y, f32::min, f32::MAX);

    assert_eq!((left, top), (box_.x, box_.y));
    assert_eq!(extreme(|it| it.x, f32::max, f32::MIN) - left, box_.width);
    assert_eq!(extreme(|it| it.y, f32::max, f32::MIN) - top, box_.height);
}

#[test]
fn a_centroid_is_the_middle_of_its_hexagon() {
    for (index, hexagon) in mark::hexagons().iter().enumerate() {
        let radius = distance(hexagon.centroid, hexagon.points[0]);

        for point in hexagon.points {
            let reach = distance(hexagon.centroid, point);

            assert!(
                (reach - radius).abs() < 0.01,
                "hexagon {index} reaches {reach} at one vertex and {radius} at another"
            );
        }
    }
}

#[test]
fn a_resting_mark_fills_the_span_it_is_given() {
    let placed: Vec<Point> = mark::hexagons()
        .iter()
        .flat_map(|it| it.place(CENTER, SPAN, 0.0, 0.0).points)
        .collect();

    let left = placed.iter().map(|it| it.x).fold(f32::MAX, f32::min);
    let right = placed.iter().map(|it| it.x).fold(f32::MIN, f32::max);

    assert_eq!(right - left, SPAN);
    assert_eq!((left + right) / 2.0, CENTER.x);
}

#[test]
fn a_resting_mark_holds_the_still_shape() {
    for (index, hexagon) in mark::hexagons().iter().enumerate() {
        let placed = hexagon.place(CENTER, SPAN, 0.0, 41.75);

        assert_eq!(
            placed.points,
            hexagon.points.map(onto_canvas),
            "hexagon {index}"
        );
    }
}

#[test]
fn the_pulse_moves_a_vertex_about_its_centroid() {
    for (index, hexagon) in mark::hexagons().iter().enumerate() {
        let centroid = onto_canvas(hexagon.centroid);
        let still = hexagon.place(CENTER, SPAN, 0.0, 41.75);
        let moved = hexagon.place(CENTER, SPAN, 1.0, 41.75);
        let grow = hexagon.pulse.scale_at(1.0, 41.75) as f32;

        for (still, moved) in still.points.into_iter().zip(moved.points) {
            let expected = distance(centroid, still) * grow;
            let reach = distance(centroid, moved);

            assert!(
                (reach - expected).abs() < 0.001,
                "hexagon {index} reaches {reach} and the pulse asks for {expected}"
            );
        }
    }
}

#[test]
fn the_stroke_follows_the_canvas_and_not_the_pulse() {
    let scale = SPAN / mark::bounds().width;

    for (index, hexagon) in mark::hexagons().iter().enumerate() {
        let still = hexagon.place(CENTER, SPAN, 0.0, 41.75);
        let moved = hexagon.place(CENTER, SPAN, 1.0, 41.75);

        assert_eq!(
            still.stroke_width,
            hexagon.stroke_width * scale,
            "hexagon {index}"
        );
        assert_eq!(moved.stroke_width, still.stroke_width, "hexagon {index}");
    }
}

/// The mark in motion, against the `mpv` overlay that drew it before.
///
/// `media-operator`'s `display/logo.lua` places the same fourteen hexagons on
/// the same canvas from the same two inputs, so the two implementations can be
/// compared exactly rather than by eye. These vertices came out of that module
/// at three moments: the mark at rest, the mark at full swing, and the mark
/// part way down a ramp. A moving hexagon that lands somewhere else is a
/// difference a person watching the screen would see as a change of rhythm.
///
/// The canvas is 1920 by 1080, where the mark takes a third of the width and
/// centers on the middle of the screen.
#[test]
fn the_mark_lands_where_the_overlay_lands_it() {
    const CENTER: Point = Point::new(960.0, 540.0);
    const SPAN: f32 = 640.0;

    // Each row is a hexagon, a moment, and that hexagon's first three
    // vertices, in the order `liken.svg` writes them.
    let expected = [
        (
            0,
            0.0,
            0.0,
            [(956.45, 453.94), (1027.42, 494.92), (1027.42, 576.88)],
        ),
        (
            0,
            1.0,
            3.7,
            [(956.45, 459.78), (1022.36, 497.84), (1022.36, 573.96)],
        ),
        (
            0,
            0.4,
            12.25,
            [(956.45, 453.91), (1027.45, 494.90), (1027.45, 576.90)],
        ),
        (
            7,
            0.0,
            0.0,
            [(1216.13, 316.30), (1276.50, 351.09), (1276.50, 420.76)],
        ),
        (
            7,
            1.0,
            3.7,
            [(1216.13, 310.61), (1281.44, 348.24), (1281.44, 423.60)],
        ),
        (
            7,
            0.4,
            12.25,
            [(1216.13, 318.91), (1274.24, 352.39), (1274.24, 419.45)],
        ),
    ];

    for (index, energy, phase, vertices) in expected {
        let placed = mark::hexagons()[index].place(CENTER, SPAN, energy, phase);

        for (drawn, (x, y)) in placed.points.iter().zip(vertices) {
            assert!(
                (drawn.x - x).abs() < 0.01 && (drawn.y - y).abs() < 0.01,
                "hexagon {index} at energy {energy} phase {phase} \
                 draws {drawn:?} where the overlay draws ({x}, {y})"
            );
        }
    }
}
