//! An age in the fewest characters that still read at a glance: "12m",
//! "5h", "4d".  A pod's age and a workload's last rollout both show
//! this way, so one tile and one card read alike.

/// Minutes under an hour, hours under two days, days under two years,
/// then years and days, as `kubectl get` prints them.  Two days, not
/// one, so "30h" shows a pod started yesterday afternoon rather than
/// rounding it down to "1d".
pub fn age(minutes: i64) -> String {
    const DAY: i64 = 1440;
    const YEAR: i64 = 365 * DAY;
    match minutes {
        ..60 => format!("{}m", minutes.max(0)),
        60..2880 => format!("{}h", minutes / 60),
        _ if minutes < 2 * YEAR => format!("{}d", minutes / DAY),
        _ if minutes < 8 * YEAR => format!("{}y{}d", minutes / YEAR, minutes % YEAR / DAY),
        _ => format!("{}y", minutes / YEAR),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::just_started(0, "0m")]
    #[case::minutes(59, "59m")]
    #[case::an_hour(60, "1h")]
    #[case::a_day_and_a_half(36 * 60, "36h")]
    #[case::two_days(2 * 1440, "2d")]
    #[case::weeks(17 * 1440 + 300, "17d")]
    #[case::a_year_and_more(400 * 1440, "400d")]
    #[case::years_and_days((6 * 365 + 31) * 1440, "6y31d")]
    #[case::many_years(9 * 365 * 1440, "9y")]
    #[case::skewed_clock(-5, "0m")]
    fn reads_at_a_glance(#[case] minutes: i64, #[case] text: &str) {
        assert_eq!(age(minutes), text);
    }
}
