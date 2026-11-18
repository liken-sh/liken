// This crate is the only implementation of the pulse, so the expected
// values below are computed from `PHI`, `RATE_MIN`, and `RATE_SPAN`,
// with no other implementation to copy them from.

use liken_iced::mark;
use liken_iced::pulse::{PHI, Pulse, RATE_MIN, RATE_SPAN, SWING};

/// The last bits of a sine can differ between platforms, so the tests
/// compare within a tolerance far below the precision of an `f32` vertex.
const TOLERANCE: f64 = 1e-12;

// For hexagons 0, 6, and 13: the first rate and the second rate in cycles
// a second, and the offset in radians. The other tests check the
// properties of the remaining hexagons.
const NUMBERS: [(usize, f64, f64, f64); 3] = [
    (0, 0.331246117975, 0.535967477525, 3.883222077451),
    (6, 0.278722825825, 0.450983005625, 2.049813313438),
    (13, 0.337445651650, 0.545998533725, 4.099626626876),
];

#[test]
fn a_hexagon_takes_its_rates_and_offset_from_its_index() {
    for (index, first_rate, second_rate, offset) in NUMBERS {
        let pulse = Pulse::for_index(index);
        let measured = [pulse.first_rate, pulse.second_rate, pulse.offset];

        for (measured, expected) in measured.into_iter().zip([first_rate, second_rate, offset]) {
            assert!(
                (measured - expected).abs() < TOLERANCE,
                "hexagon {index} has {measured}, want {expected}"
            );
        }
    }
}

#[test]
fn each_hexagon_of_the_mark_has_the_pulse_for_its_index() {
    for (index, hexagon) in mark::hexagons().iter().enumerate() {
        assert_eq!(hexagon.pulse, Pulse::for_index(index), "hexagon {index}");
    }
}

#[test]
fn every_first_rate_is_within_the_rate_range() {
    for index in 0..mark::hexagons().len() {
        let rate = Pulse::for_index(index).first_rate;

        assert!(
            (RATE_MIN..=RATE_MIN + RATE_SPAN).contains(&rate),
            "hexagon {index} has first rate {rate}, want a rate in the range"
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

// Each row is a phase, an energy, and the scales of hexagons 0, 6, and 13.
// The scales come from the two-sine formula computed to sixty digits with
// the exact golden ratio and rounded to `f64`. They pin `Pulse::scale_at`
// to twelve decimal places.
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
fn the_scale_is_the_mean_of_the_two_sines() {
    for (phase, energy, expected) in MOMENTS {
        for (index, expected) in [0, 6, 13].into_iter().zip(expected) {
            let scale = Pulse::for_index(index).scale_at(energy, phase);

            assert!(
                (scale - expected).abs() < TOLERANCE,
                "hexagon {index} at phase {phase} and energy {energy} \
                 scales to {scale}, want {expected}"
            );
        }
    }
}

#[test]
fn at_energy_zero_every_hexagon_has_scale_one() {
    for phase in [0.0, 1.0, 3.5, 12.25, 900.0] {
        for index in 0..mark::hexagons().len() {
            let scale = Pulse::for_index(index).scale_at(0.0, phase);

            assert_eq!(scale, 1.0, "hexagon {index} at phase {phase}");
        }
    }
}

#[test]
fn at_full_energy_the_scale_stays_within_the_swing() {
    for phase in [0.0, 0.25, 1.0, 3.5, 12.25, 900.0] {
        for index in 0..mark::hexagons().len() {
            let scale = Pulse::for_index(index).scale_at(1.0, phase);

            assert!(
                (scale - 1.0).abs() <= SWING,
                "hexagon {index} at phase {phase} scales to {scale}, want within {SWING} of 1.0"
            );
        }
    }
}

#[test]
fn a_different_phase_gives_a_different_scale() {
    let pulse = Pulse::for_index(0);

    assert_ne!(pulse.scale_at(1.0, 0.0), pulse.scale_at(1.0, 1.0));
}
