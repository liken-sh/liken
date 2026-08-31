//! The motion of the mark, as `motion.md` states it.
//!
//! Each hexagon changes size about its own center, up to ten percent at full
//! energy. The rate and the phase come from the hexagon's index, so the same
//! moment draws the same frame on every run and on every surface.
//!
//! The brand's `motion.md` states the ten percent swing and the
//! index-derived phases, and this module states the rest: the rates, the
//! golden-ratio second sine, and the mean of the two. This module is the
//! definition every surface that animates the mark must match, and a
//! change to any number here is a change a person watching a screen
//! sees.

/// The golden ratio, to the ten decimal places this module chooses to
/// carry.
///
/// A hexagon's spread is its index times this number, taken modulo one. That
/// places the fourteen spreads across the range with no two close together, so
/// the fourteen rates and offsets all differ and the mosaic reads as fourteen
/// independent parts rather than one block. A hexagon's second rate is its
/// first times this number, so its two sines share no common period short
/// enough to see.
pub const PHI: f64 = 1.6180339887;

/// The slowest first rate, in cycles a second at full energy.
pub const RATE_MIN: f64 = 0.22;

/// The distance from the slowest first rate to the fastest, so a first rate
/// falls between 0.22 and 0.40 cycles a second. About a third of a hertz reads
/// as a slow swell.
pub const RATE_SPAN: f64 = 0.18;

/// The size change at full energy, as a fraction of the still size.
pub const SWING: f64 = 0.10;

/// One hexagon's motion: two sine rates in cycles a second, and the phase
/// offset each sine starts at, in radians.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Pulse {
    /// The rate of the first sine.
    pub first_rate: f64,
    /// The rate of the second sine, the first times [`PHI`].
    pub second_rate: f64,
    /// The phase offset of the first sine.
    pub first_offset: f64,
    /// The phase offset of the second sine.
    pub second_offset: f64,
}

impl Pulse {
    /// The motion of the hexagon at `index`, counting from zero in the order
    /// `liken.svg` writes the polygons.
    ///
    /// The spread is the one-based ordinal times [`PHI`] modulo one, so a
    /// zero-based index adds one first. The ordinal is this module's own
    /// definition: hexagon one is the first polygon in the file.
    pub fn for_index(index: usize) -> Self {
        let ordinal = (index + 1) as f64;
        let spread = (ordinal * PHI) % 1.0;
        let first_rate = RATE_MIN + RATE_SPAN * spread;

        Self {
            first_rate,
            second_rate: first_rate * PHI,
            first_offset: std::f64::consts::TAU * spread,
            second_offset: std::f64::consts::TAU * ((ordinal * PHI * PHI) % 1.0),
        }
    }

    /// The size of the hexagon at one moment, as a multiple of its still size.
    ///
    /// `energy` runs from 0 at rest to 1 at full swing, and `phase` is the
    /// animation clock in seconds. The mean of the two sines stays within -1
    /// and 1, so the size runs from [`SWING`] below the still size to [`SWING`]
    /// above it at full energy, and the energy scales that swing down to
    /// nothing at rest.
    ///
    /// At energy 0 the result is exactly 1.0, so a resting mark draws the still
    /// shape.
    pub fn scale_at(&self, energy: f64, phase: f64) -> f64 {
        let tau = std::f64::consts::TAU;
        let swing = 0.5
            * ((tau * self.first_rate * phase + self.first_offset).sin()
                + (tau * self.second_rate * phase + self.second_offset).sin());

        1.0 + SWING * energy * swing
    }
}
