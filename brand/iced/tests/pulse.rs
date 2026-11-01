// This crate defines the motion, so the numbers below are arithmetic
// this file works out from PHI, RATE_MIN, and RATE_SPAN, and not
// readings taken off another implementation.

use liken_iced::mark;
use liken_iced::pulse::{PHI, Pulse, RATE_MIN, RATE_SPAN, SWING};

/// A sine is a library function whose last bits belong to the platform, and the
/// numbers below are written to twelve decimal places. Both comparisons
/// therefore take a tolerance. One part in a million million is far under what
/// an `f32` vertex or a screen can hold.
const TOLERANCE: f64 = 1e-12;

// The rates and the offset of the first hexagon, the seventh, and the
// last, in cycles a second and in radians. Nothing in the motion is
// random, so each value is arithmetic, and the properties below hold for
// the eleven hexagons this table leaves out.
const NUMBERS: [(usize, f64, f64, f64); 3] = [
    (0, 0.331246117975, 0.535967477525, 3.883222077451),
    (6, 0.278722825825, 0.450983005625, 2.049813313438),
    (13, 0.337445651650, 0.545998533725, 4.099626626876),
];

#[test]
fn a_hexagon_runs_the_rates_and_the_offset_its_ordinal_gives() {
    for (index, first_rate, second_rate, offset) in NUMBERS {
        let pulse = Pulse::for_index(index);
        let measured = [pulse.first_rate, pulse.second_rate, pulse.offset];

        for (measured, expected) in measured.into_iter().zip([first_rate, second_rate, offset]) {
            assert!(
                (measured - expected).abs() < TOLERANCE,
                "hexagon {index} runs at {measured}, and the numbers state {expected}"
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

// The scale of hexagons 0, 6, and 13 at four moments, as phase, energy,
// and the three scales. Each value is the two-sine arithmetic worked out
// at sixty digits from the exact golden ratio and rounded to an `f64`, and
// it holds `Pulse::scale_at` to twelve decimal places.
const MOMENTS: [(f64, f64, [f64; 3]); 4] = [
    (
        0.0,
        0.5,
        [0.9662254852869239, 1.0443724214622627, 0.9590968784890017],
    ),
    (
        1.0,
        1.0,
        [1.0255111082334663, 0.9200939279919017, 1.0442383946885916],
    ),
    (
        3.5,
        0.5,
        [0.9763242340846764, 1.0095953060086877, 0.9686574911213127],
    ),
    (
        12.25,
        1.0,
        [1.001052100036052, 0.9097809414837629, 0.9933411169596843],
    ),
];

#[test]
fn a_moment_renders_the_mean_of_the_two_sines() {
    for (phase, energy, expected) in MOMENTS {
        for (index, expected) in [0, 6, 13].into_iter().zip(expected) {
            let scale = Pulse::for_index(index).scale_at(energy, phase);

            assert!(
                (scale - expected).abs() < TOLERANCE,
                "hexagon {index} at phase {phase} and energy {energy} \
                 scales to {scale}, and the arithmetic states {expected}"
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
