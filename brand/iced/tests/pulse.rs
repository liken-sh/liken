//! The motion is a port of `display/logo.lua`, the `mpv` overlay that draws the
//! same mark on the same screens. The two run side by side on one cluster, so
//! the numbers below are the Lua's own, and a difference in any of them is a
//! difference a person sees.

use liken_iced::mark;
use liken_iced::pulse::{PHI, Pulse, RATE_MIN, RATE_SPAN, SWING};

/// The rates and the offsets `logo.lua` gives the fourteen hexagons, in the
/// order the file writes them. Nothing in the motion is random, so these are
/// arithmetic and a test compares them exactly.
const NUMBERS: [(f64, f64, f64, f64); 14] = [
    (
        0.331246117966,
        0.5359674774939177,
        3.883222077137434,
        3.8832220764364296,
    ),
    (
        0.262492235932,
        0.4247213595078354,
        1.4832588470952817,
        1.483258845693273,
    ),
    (
        0.37373835389800003,
        0.6047213594877532,
        5.366480924232717,
        5.3664809221297,
    ),
    (
        0.30498447186399996,
        0.49347524150167077,
        2.9665176941905633,
        2.966517691386546,
    ),
    (
        0.2362305898299999,
        0.38222912351558835,
        0.5665544641484097,
        0.5665544606433864,
    ),
    (
        0.347476707796,
        0.5622291234955062,
        4.449776541285848,
        4.4497765370798135,
    ),
    (
        0.2787228257619998,
        0.45098300550942366,
        2.0498133112436885,
        2.0498133063366537,
    ),
    (
        0.3899689437279999,
        0.6309830054893415,
        5.933035388381127,
        5.933035382773092,
    ),
    (
        0.321215061694,
        0.5197368875032594,
        3.5330721583389786,
        3.5330721520299435,
    ),
    (
        0.2524611796599998,
        0.4084907695171768,
        1.1331089282968194,
        1.1331089212867729,
    ),
    (
        0.36370729762599957,
        0.5884907694970941,
        5.016331005434246,
        5.0163309977231885,
    ),
    (
        0.294953415592,
        0.47724465151101253,
        2.6163677753921095,
        2.6163677669800403,
    ),
    (
        0.2261995335579998,
        0.3659985335249299,
        0.2164045453499502,
        0.21640453623689204,
    ),
    (
        0.3374456515239996,
        0.5459985335048473,
        4.099626622487377,
        4.099626612673307,
    ),
];

#[test]
fn every_hexagon_runs_the_rates_and_offsets_the_mpv_screen_runs() {
    for (index, expected) in NUMBERS.iter().enumerate() {
        let pulse = Pulse::for_index(index);

        assert_eq!(
            (
                pulse.first_rate,
                pulse.second_rate,
                pulse.first_offset,
                pulse.second_offset
            ),
            *expected,
            "hexagon {index}"
        );
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
    for index in 0..NUMBERS.len() {
        let rate = Pulse::for_index(index).first_rate;

        assert!(
            (RATE_MIN..=RATE_MIN + RATE_SPAN).contains(&rate),
            "hexagon {index} runs at {rate}"
        );
    }
}

#[test]
fn the_second_rate_is_the_first_times_the_golden_ratio() {
    for index in 0..NUMBERS.len() {
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

/// A sine is a library function whose last bits belong to the platform, so the
/// comparison takes a tolerance. It is one part in a million million, far under
/// what an `f32` vertex or a screen can hold.
const TOLERANCE: f64 = 1e-12;

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
        for index in 0..NUMBERS.len() {
            let scale = Pulse::for_index(index).scale_at(0.0, phase);

            assert_eq!(scale, 1.0, "hexagon {index} at phase {phase}");
        }
    }
}

#[test]
fn the_swing_never_passes_ten_percent() {
    for phase in [0.0, 0.25, 1.0, 3.5, 12.25, 900.0] {
        for index in 0..NUMBERS.len() {
            let scale = Pulse::for_index(index).scale_at(1.0, phase);

            assert!(
                (scale - 1.0).abs() <= SWING,
                "hexagon {index} at phase {phase} scales to {scale}"
            );
        }
    }
}

#[test]
fn a_moment_renders_the_same_scale_twice() {
    for index in 0..NUMBERS.len() {
        let pulse = Pulse::for_index(index);

        assert_eq!(
            pulse.scale_at(0.6, 41.75),
            pulse.scale_at(0.6, 41.75),
            "hexagon {index}"
        );
    }
}

#[test]
fn the_phase_moves_the_mark() {
    let pulse = Pulse::for_index(0);

    assert_ne!(pulse.scale_at(1.0, 0.0), pulse.scale_at(1.0, 1.0));
}
