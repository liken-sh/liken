use super::*;

const SHADE: Color = Color::from_rgba(0.0, 0.0, 0.0, 0.94);
const CLEAR: Color = Color::from_rgba(0.0, 0.0, 0.0, 0.0);
const SCRIM: [Stop; 3] = [(0.0, SHADE), (0.62, SHADE), (1.0, CLEAR)];

fn alpha(share: f32) -> f32 {
    at(share, &SCRIM)[3]
}

// A tile of the scrim across a run of this many panel pixels, at this
// density, as its alpha bytes row by row.
fn tile(run: u32, across: u32, density: f32) -> Vec<Vec<u8>> {
    let key = Key {
        axis: Axis::Across,
        run,
        along: run,
        across,
        density: density.to_bits(),
        stops: bits(&SCRIM),
    };
    rgba(&key, &SCRIM)
        .chunks(4 * run as usize)
        .map(|row| row.chunks(4).map(|texel| texel[3]).collect())
        .collect()
}

#[test]
fn a_ramp_holds_its_first_stop_until_the_next() {
    assert_eq!(alpha(0.0), 0.94);
    assert_eq!(alpha(0.3), 0.94);
    assert_eq!(alpha(0.62), 0.94);
}

#[test]
fn a_ramp_eases_between_two_stops() {
    // Halfway between the hold and the end, a smoothstep is halfway.
    assert!((alpha(0.81) - 0.47).abs() < 1e-4);
    // A quarter of the way in, a smoothstep has moved less than a
    // quarter of the drop.
    assert!(alpha(0.715) > 0.94 - 0.94 / 4.0);
}

#[test]
fn a_ramp_holds_its_last_stop_past_the_end() {
    assert_eq!(alpha(1.0), 0.0);
    assert_eq!(alpha(1.5), 0.0);
}

#[test]
fn a_ramp_with_no_stops_is_clear() {
    assert_eq!(at(0.5, &[]), [0.0; 4]);
}

#[test]
fn a_colored_stop_keeps_its_color_within_the_noise() {
    let orange = Color::from_rgba8(230, 126, 34, 0.5);
    let texel = encode(at(0.5, &[(0.0, orange), (1.0, orange)]), 0.5);
    assert_eq!(texel, [230, 126, 34, 128]);
}

// Every texel carries the value of its column within the noise and the
// rounding: 0.3 of a step and half a step.
#[test]
fn each_texel_carries_its_columns_value_within_the_noise() {
    for density in [1.0, 2.0] {
        for row in tile(200, 16, density) {
            for (column, alpha) in row.into_iter().enumerate() {
                let value = at((column as f32 + 0.5) / 200.0, &SCRIM)[3] * 255.0;
                assert!(
                    (f32::from(alpha) - value).abs() <= 0.8,
                    "{alpha} for {value}"
                );
            }
        }
    }
}

// A column whose value falls near the middle of two steps rounds up on
// some pixels and down on others, which is the dither that keeps the
// falloff from showing as bands.
#[test]
fn a_column_between_two_steps_carries_both() {
    let rows = tile(400, 64, 1.0);
    let between: Vec<usize> = (0..400)
        .filter(|column| {
            let value = at((*column as f32 + 0.5) / 400.0, &SCRIM)[3] * 255.0;
            (value.fract() - 0.5).abs() < 0.1
        })
        .collect();
    assert!(!between.is_empty());
    for column in between {
        let mut levels: Vec<u8> = rows.iter().map(|row| row[column]).collect();
        levels.sort_unstable();
        levels.dedup();
        assert_eq!(levels.len(), 2, "column {column}");
    }
}

#[test]
fn a_span_divides_into_whole_tiles_where_it_can() {
    assert_eq!(
        spans(1080, TILE),
        (0..9).map(|n| (n * 120, 120)).collect::<Vec<_>>()
    );
    assert_eq!(spans(3840, TILE).len(), 30);
    assert_eq!(spans(40, TILE), vec![(0, 40)]);
}

#[test]
fn a_span_no_tile_divides_ends_on_a_shorter_tile() {
    let pieces = spans(1031, TILE);
    assert_eq!(pieces.iter().map(|(_, length)| length).sum::<u32>(), 1031);
    assert_eq!(pieces.last(), Some(&(8 * 115, 111)));
    assert!(pieces.iter().all(|(_, length)| *length <= TILE));
}

#[test]
fn the_same_tile_is_the_same_picture() {
    let key = |run| Key {
        axis: Axis::Across,
        run,
        along: run,
        across: 8,
        density: 1.0f32.to_bits(),
        stops: bits(&SCRIM),
    };
    let first = picture(key(64), &SCRIM);
    assert_eq!(picture(key(64), &SCRIM).id(), first.id());
    assert_ne!(picture(key(65), &SCRIM).id(), first.id());
}

#[test]
fn a_density_that_is_no_scale_is_ignored() {
    density(2.0);
    density(0.0);
    density(f32::NAN);
    assert_eq!(DENSITY.with(Cell::get), 2.0);
    density(1.0);
}

// A solid takes no noise, the way the toolkit fills a solid, so its one
// texel is the color rounded once.
#[test]
fn a_solid_is_one_texel_with_no_noise() {
    let ground = Color::from_rgba(0.0, 0.0, 0.0, 0.9);
    let key = Key {
        axis: Axis::Down,
        run: 1,
        along: 1,
        across: 1,
        density: 0,
        stops: bits(&[(0.0, ground), (1.0, ground)]),
    };
    assert_eq!(
        rgba(&key, &[(0.0, ground), (1.0, ground)]),
        vec![0, 0, 0, 230]
    );
}
