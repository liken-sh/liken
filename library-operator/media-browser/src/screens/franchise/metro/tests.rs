// The strip's measures over invented rows: its width, the colors, the
// runs, and where the dots stand.

use super::*;
use crate::catalog::franchise::Standing;
use crate::screens::franchise::wall::{Cell, HEAD, THIN, card_height};

fn row(universes: &[usize], held: bool) -> Row {
    Row {
        cell: Cell {
            universes: universes.to_vec(),
            standing: match held {
                true => Standing::Held,
                false => Standing::Missing,
            },
            ..Cell::default()
        },
        ..Row::default()
    }
}

#[test]
fn the_strip_takes_a_pitch_per_universe_and_nothing_for_one() {
    assert_eq!(width(8), 8.0 * PITCH);
    assert_eq!(width(2), 2.0 * PITCH);
    assert_eq!(width(1), 0.0);
    assert_eq!(width(0), 0.0);
}

#[test]
fn a_line_stands_in_the_middle_of_its_pitch() {
    let strip = area(300.0, 0.0, width(3), 900.0);
    assert_eq!(line_x(strip, 0), 316.0);
    assert_eq!(line_x(strip, 2), 300.0 + 2.5 * PITCH);
}

#[test]
fn eight_universes_take_eight_distinct_hues_and_the_first_takes_the_first() {
    let colors: Vec<Color> = (0..8).map(|index| color(index, 8)).collect();
    for (index, one) in colors.iter().enumerate() {
        for other in &colors[index + 1..] {
            let apart = (one.r - other.r).abs() + (one.g - other.g).abs() + (one.b - other.b).abs();
            assert!(apart > 0.15, "{one:?} {other:?}");
        }
    }
    assert_eq!(color(0, 8), color(0, 2));
    assert_eq!(color(0, 8), oklch(LIGHTNESS, CHROMA, FIRST_HUE));
    assert_eq!(color(1, 2), oklch(LIGHTNESS, CHROMA, FIRST_HUE + 180.0));
}

#[test]
fn every_line_color_is_light_enough_to_read_on_black() {
    for index in 0..8 {
        let color = color(index, 8);
        assert!(color.r + color.g + color.b > 1.5, "{color:?}");
        assert!(color.r <= 1.0 && color.g <= 1.0 && color.b <= 1.0);
    }
}

#[test]
fn a_run_reaches_from_a_universes_first_row_to_its_last() {
    let rows = vec![
        row(&[0], true),
        row(&[1], false),
        row(&[0, 1, 2], true),
        row(&[0], true),
    ];
    assert_eq!(
        runs(&rows, 4),
        [Some((0, 3)), Some((1, 2)), Some((2, 2)), None]
    );
    assert_eq!(runs(&[], 2), [None, None]);
    assert_eq!(runs(&[row(&[9], true)], 1), [None]);
}

#[test]
fn a_dot_stands_in_the_middle_of_its_row_and_scrolls_with_the_wall() {
    let art = 100.0;
    let rows = vec![row(&[0], true), row(&[1], false)];
    let tops = crate::screens::franchise::wall::tops(&rows, art);
    let strip = area(300.0, 200.0, width(2), 900.0);
    assert_eq!(
        middle(strip, 0, &tops, 0.0),
        strip.y + HEAD + card_height(art) / 2.0
    );
    assert_eq!(
        middle(strip, 1, &tops, 50.0),
        strip.y + tops[1] + THIN / 2.0 - 50.0
    );
}
#[test]
fn black_encodes_as_black_and_white_as_white() {
    let black = oklch(0.0, 0.0, 0.0);
    assert!(
        black.r < 0.01 && black.g < 0.01 && black.b < 0.01,
        "{black:?}"
    );
    let white = oklch(1.0, 0.0, 0.0);
    assert!(
        white.r > 0.99 && white.g > 0.99 && white.b > 0.99,
        "{white:?}"
    );
}
