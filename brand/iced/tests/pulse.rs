//! The motion is a port of `display/logo.lua`, the `mpv` overlay that draws the
//! same mark on the same screens. The two run side by side on one cluster, so
//! the numbers below are the Lua's own, and a difference in any of them is a
//! difference a person sees.

use liken_iced::mark;
use liken_iced::pulse::{PHI, Pulse, RATE_MIN, RATE_SPAN, SWING};

/// A sine is a library function whose last bits belong to the platform, and the
/// numbers below are written to twelve decimal places. Both comparisons
/// therefore take a tolerance. One part in a million million is far under what
/// an `f32` vertex or a screen can hold.
const TOLERANCE: f64 = 1e-12;

/// The rates and the offsets `logo.lua` gives the first hexagon, the seventh,
/// and the last, in cycles a second and in radians. Nothing in the motion is
/// random, so these are arithmetic, and the properties below hold for the
/// eleven this table leaves out.
const NUMBERS: [(usize, f64, f64, f64, f64); 3] = [
    (
        0,
        0.331246117966,
        0.535967477494,
        3.883222077137,
        3.883222076436,
    ),
    (
        6,
        0.278722825762,
        0.450983005509,
        2.049813311244,
        2.049813306337,
    ),
    (
        13,
        0.337445651524,
        0.545998533505,
        4.099626622487,
        4.099626612673,
    ),
];

#[test]
fn every_hexagon_runs_the_rates_and_offsets_the_mpv_screen_runs() {
    for (index, first_rate, second_rate, first_offset, second_offset) in NUMBERS {
        let pulse = Pulse::for_index(index);
        let measured = [
            pulse.first_rate,
            pulse.second_rate,
            pulse.first_offset,
            pulse.second_offset,
        ];

        for (measured, expected) in
            measured
                .into_iter()
                .zip([first_rate, second_rate, first_offset, second_offset])
        {
            assert!(
                (measured - expected).abs() < TOLERANCE,
                "hexagon {index} runs at {measured}, and the mpv screen runs it at {expected}"
            );
        }
    }
}

#[test]
fn the_mark_carries_one_pulse_for_each_hexagon() {
    for (index, hexagon) in mark::hexagons().iter().enumerate() {
        assert_eq!(hexagon.pulse, Pulse::for_index(index), "hexagon {index}");
    }
}

#[test]
fn the_first_rates_run_from_the_slowest_to_the_fastest() {
    for index in 0..mark::hexagons().len() {
        let rate = Pulse::for_index(index).first_rate;

        assert!(
            (RATE_MIN..=RATE_MIN + RATE_SPAN).contains(&rate),
            "hexagon {index} runs at {rate}"
        );
    }
}

#[test]
fn the_second_rate_is_the_first_times_the_golden_ratio() {
    for index in 0..mark::hexagons().len() {
        let pulse = Pulse::for_index(index);

        assert_eq!(pulse.second_rate, pulse.first_rate * PHI, "hexagon {index}");
    }
}

/// The scale of hexagons 0, 6, and 13 at four moments, as phase, energy, and
/// the three scales. The values come from `logo.lua`'s own expressions.
const MOMENTS: [(f64, f64, [f64; 3]); 4] = [
    (
        0.0,
        0.5,
        [0.9662254853114045, 1.044372421569378, 0.9590968787563228],
    ),
    (
        1.0,
        1.0,
        [1.0255111081815917, 0.9200939280276096, 1.0442383941822317],
    ),
    (
        3.5,
        0.5,
        [0.9763242341248957, 1.0095953058381228, 0.9686574914705058],
    ),
    (
        12.25,
        1.0,
        [1.0010520999896813, 0.9097809410311032, 0.9933411176438125],
    ),
];

#[test]
fn a_moment_renders_the_scales_the_mpv_screen_renders() {
    for (phase, energy, expected) in MOMENTS {
        for (index, expected) in [0, 6, 13].into_iter().zip(expected) {
            let scale = Pulse::for_index(index).scale_at(energy, phase);

            assert!(
                (scale - expected).abs() < TOLERANCE,
                "hexagon {index} at phase {phase} and energy {energy} \
                 scales to {scale}, and the mpv screen scales it to {expected}"
            );
        }
    }
}

#[test]
fn a_resting_mark_holds_every_hexagon_at_its_still_size() {
    for phase in [0.0, 1.0, 3.5, 12.25, 900.0] {
        for index in 0..mark::hexagons().len() {
            let scale = Pulse::for_index(index).scale_at(0.0, phase);

            assert_eq!(scale, 1.0, "hexagon {index} at phase {phase}");
        }
    }
}

#[test]
fn the_swing_never_passes_ten_percent() {
    for phase in [0.0, 0.25, 1.0, 3.5, 12.25, 900.0] {
        for index in 0..mark::hexagons().len() {
            let scale = Pulse::for_index(index).scale_at(1.0, phase);

            assert!(
                (scale - 1.0).abs() <= SWING,
                "hexagon {index} at phase {phase} scales to {scale}"
            );
        }
    }
}

#[test]
fn the_phase_moves_the_mark() {
    let pulse = Pulse::for_index(0);

    assert_ne!(pulse.scale_at(1.0, 0.0), pulse.scale_at(1.0, 1.0));
}
