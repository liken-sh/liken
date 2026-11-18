//! Kubernetes quantities, such as `250m`, `1.5Gi`, or `1e3`, parsed into
//! exact amounts.
//!
//! An `Amount` stores billionths of a unit in an integer, so a sum of
//! many pods' requests equals the total that the API server and kubectl
//! compute, with no floating-point drift.  A billionth is the smallest
//! suffix that Kubernetes accepts (`n`).  A finer value, written with an
//! exponent, rounds up to the next billionth, as Kubernetes rounds up
//! when it scales a quantity down.

use std::iter::Sum;
use std::ops::{Add, Sub};

const NANOS_PER_UNIT: f64 = 1e9;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord)]
pub struct Amount(i128);

impl Amount {
    #[cfg(test)]
    pub const fn from_nanos(nanos: i128) -> Self {
        Self(nanos)
    }

    /// Parses a quantity in Kubernetes' syntax: a decimal number, then a
    /// binary suffix (`Ki` to `Ei`), a decimal suffix (`n`, `u`, `m`,
    /// `k`, `M` to `E`), or a decimal exponent (`e3`, `E-2`).  Returns
    /// `None` for text the API server would also reject, or for an
    /// amount too large to hold.
    pub fn parse(text: &str) -> Option<Self> {
        let text = text.trim();
        let (negative, unsigned) = match text.as_bytes().first()? {
            b'-' => (true, &text[1..]),
            b'+' => (false, &text[1..]),
            _ => (false, text),
        };
        let number_len = unsigned
            .find(|c: char| !(c.is_ascii_digit() || c == '.'))
            .unwrap_or(unsigned.len());
        let (number, suffix) = unsigned.split_at(number_len);
        let (whole, fraction) = number.split_once('.').unwrap_or((number, ""));
        if whole.is_empty() && fraction.is_empty() || fraction.contains('.') {
            return None;
        }
        let (power_of_two, power_of_ten) = scale(suffix)?;

        let mut digits: i128 = 0;
        for digit in whole.bytes().chain(fraction.bytes()) {
            digits = digits
                .checked_mul(10)?
                .checked_add(i128::from(digit - b'0'))?;
        }
        let numerator = digits.checked_mul(2i128.checked_pow(power_of_two)?)?;
        let exponent = 9 + power_of_ten - i32::try_from(fraction.len()).ok()?;
        let nanos = if exponent >= 0 {
            numerator.checked_mul(10i128.checked_pow(exponent.unsigned_abs())?)?
        } else {
            let divisor = 10i128.checked_pow(exponent.unsigned_abs())?;
            // This division rounds up only because the numerator is not
            // negative.  The sign is applied after it.
            numerator.checked_add(divisor - 1)? / divisor
        };
        Some(Self(if negative { -nanos } else { nanos }))
    }

    /// The amount in whole units: cores for CPU, bytes for memory.
    pub fn units(self) -> f64 {
        self.0 as f64 / NANOS_PER_UNIT
    }

    /// The amount in thousandths of a unit, rounded up, as Kubernetes'
    /// `MilliValue` rounds it: millicores for CPU.
    pub fn millis(self) -> i128 {
        self.0.div_euclid(1_000_000) + i128::from(self.0.rem_euclid(1_000_000) != 0)
    }
}

/// The power of two and the power of ten that `suffix` multiplies by.
fn scale(suffix: &str) -> Option<(u32, i32)> {
    let scale = match suffix {
        "" => (0, 0),
        "Ki" => (10, 0),
        "Mi" => (20, 0),
        "Gi" => (30, 0),
        "Ti" => (40, 0),
        "Pi" => (50, 0),
        "Ei" => (60, 0),
        "n" => (0, -9),
        "u" => (0, -6),
        "m" => (0, -3),
        "k" => (0, 3),
        "M" => (0, 6),
        "G" => (0, 9),
        "T" => (0, 12),
        "P" => (0, 15),
        "E" => (0, 18),
        exponent => {
            let digits = exponent
                .strip_prefix('e')
                .or_else(|| exponent.strip_prefix('E'))?;
            (0, digits.parse().ok()?)
        }
    };
    Some(scale)
}

impl Add for Amount {
    type Output = Self;

    fn add(self, other: Self) -> Self {
        Self(self.0.saturating_add(other.0))
    }
}

impl Sub for Amount {
    type Output = Self;

    fn sub(self, other: Self) -> Self {
        Self(self.0.saturating_sub(other.0))
    }
}

impl Sum for Amount {
    fn sum<I: Iterator<Item = Self>>(amounts: I) -> Self {
        amounts.fold(Self::default(), Add::add)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    const UNIT: i128 = 1_000_000_000;

    #[rstest]
    #[case::whole("8", 8 * UNIT)]
    #[case::zero("0", 0)]
    #[case::decimal("1.5", 3 * UNIT / 2)]
    #[case::leading_point(".5", UNIT / 2)]
    #[case::trailing_point("2.", 2 * UNIT)]
    #[case::plus_sign("+3", 3 * UNIT)]
    #[case::minus_sign("-250m", -UNIT / 4)]
    #[case::milli("250m", UNIT / 4)]
    #[case::micro("5u", 5_000)]
    #[case::nano("100n", 100)]
    #[case::kilo("2k", 2_000 * UNIT)]
    #[case::mega("128M", 128_000_000 * UNIT)]
    #[case::giga("1G", 1_000_000_000 * UNIT)]
    #[case::tera("1T", 1_000_000_000_000 * UNIT)]
    #[case::peta("1P", 10i128.pow(15) * UNIT)]
    #[case::exa("1E", 10i128.pow(18) * UNIT)]
    #[case::kibi("1Ki", 1024 * UNIT)]
    #[case::mebi("64Mi", 64 * 1024 * 1024 * UNIT)]
    #[case::gibi("1.5Gi", 3 * 512 * 1024 * 1024 * UNIT)]
    #[case::tebi("1Ti", (1 << 40) * UNIT)]
    #[case::pebi("1Pi", (1 << 50) * UNIT)]
    #[case::exbi("1Ei", (1 << 60) * UNIT)]
    #[case::exponent("1e3", 1_000 * UNIT)]
    #[case::capital_exponent("1E3", 1_000 * UNIT)]
    #[case::negative_exponent("5e-1", UNIT / 2)]
    #[case::decimal_with_exponent("1.25e2", 125 * UNIT)]
    #[case::finer_than_nano_rounds_up("1e-12", 1)]
    #[case::surrounding_space(" 4 ", 4 * UNIT)]
    fn parses(#[case] text: &str, #[case] nanos: i128) {
        assert_eq!(Amount::parse(text), Some(Amount::from_nanos(nanos)));
    }

    #[rstest]
    #[case::empty("")]
    #[case::sign_alone("-")]
    #[case::point_alone(".")]
    #[case::suffix_alone("Mi")]
    #[case::unknown_suffix("5Q")]
    #[case::lowercase_binary("5mi")]
    #[case::two_points("1.2.3")]
    #[case::bare_exponent("1e")]
    #[case::too_large("1e100")]
    fn rejects(#[case] text: &str) {
        assert_eq!(Amount::parse(text), None);
    }

    #[rstest]
    #[case::cores("1500m", 1.5)]
    #[case::bytes("1Ki", 1024.0)]
    fn in_units(#[case] text: &str, #[case] expected: f64) {
        assert_eq!(Amount::parse(text).unwrap().units(), expected);
    }

    #[rstest]
    #[case::whole("2", 2000)]
    #[case::milli("360m", 360)]
    #[case::finer_rounds_up("100n", 1)]
    #[case::negative("-250m", -250)]
    fn in_millis(#[case] text: &str, #[case] expected: i128) {
        assert_eq!(Amount::parse(text).unwrap().millis(), expected);
    }

    #[rstest]
    fn subtracts() {
        let difference = Amount::parse("4").unwrap() - Amount::parse("3640m").unwrap();
        assert_eq!(difference, Amount::parse("360m").unwrap());
    }
}
