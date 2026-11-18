//! The size animation of each hexagon of the mark.
//!
//! Each hexagon scales about its own center, by up to ten percent at
//! full energy. Its rates and phase offset are computed from its index,
//! with no random source, so the same instant draws the same frame on
//! every run and every surface.
//!
//! `motion.md` specifies the ten percent swing and the index-derived
//! phases. The rates, the golden-ratio second sine, and the averaging
//! of the two sines are defined only here. Every screen that draws the
//! mark through `mark::draw` uses these constants, so a change to one
//! changes the animation on each of them.

/// The golden ratio.
///
/// A hexagon's spread is its one-based ordinal times this number,
/// modulo one. Because the golden ratio is irrational, the fourteen
/// spreads are distinct and well separated, so the fourteen hexagons
/// animate independently. A hexagon's second rate is its first rate
/// times this number, so its two sines never line up on a short period.
pub const PHI: f64 = std::f64::consts::GOLDEN_RATIO;

/// The lowest first rate, in cycles per second.
pub const RATE_MIN: f64 = 0.22;

/// The range of first rates above [`RATE_MIN`]. A first rate is between
/// 0.22 and 0.40 cycles per second. The rates do not depend on energy.
/// `motion.md` asks for a slower pulse at lower energy, so the caller
/// slows the pulse by advancing `phase` more slowly.
pub const RATE_SPAN: f64 = 0.18;

/// The largest size change, at full energy, as a fraction of the
/// resting size.
pub const SWING: f64 = 0.10;

/// The animation of one hexagon: two sine rates in cycles per second
/// and a phase offset in radians.
///
/// Both sines use the same offset. A second offset computed from the
/// ordinal times [`PHI`] squared would be identical, because φ² = φ +
/// 1, so the two products differ by a whole number and are equal modulo
/// one. The sines still drift apart because the second runs [`PHI`]
/// times as fast as the first.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Pulse {
    /// The rate of the first sine, in cycles per second.
    pub first_rate: f64,
    /// The rate of the second sine, in cycles per second.
    pub second_rate: f64,
    /// The phase offset of both sines, in radians.
    pub offset: f64,
}

impl Pulse {
    /// The animation of the hexagon at `index`, counted from zero in
    /// the order of the polygons in `liken.svg`.
    ///
    /// The spread uses the one-based ordinal, `index + 1`, so the first
    /// polygon has spread `PHI` modulo one and not zero.
    pub fn for_index(index: usize) -> Self {
        let ordinal = (index + 1) as f64;
        let spread = (ordinal * PHI) % 1.0;
        let first_rate = RATE_MIN + RATE_SPAN * spread;

        Self {
            first_rate,
            second_rate: first_rate * PHI,
            offset: std::f64::consts::TAU * spread,
        }
    }

    /// The size of the hexagon at one instant, as a multiple of its
    /// resting size.
    ///
    /// `energy` runs from 0 at rest to 1 at full swing, and `phase` is
    /// the animation clock in seconds. The result is between
    /// `1 - SWING * energy` and `1 + SWING * energy`, so it is exactly
    /// 1.0 at energy 0.
    pub fn scale_at(&self, energy: f64, phase: f64) -> f64 {
        let tau = std::f64::consts::TAU;
        let swing = 0.5
            * ((tau * self.first_rate * phase + self.offset).sin()
                + (tau * self.second_rate * phase + self.offset).sin());

        1.0 + SWING * energy * swing
    }
}
