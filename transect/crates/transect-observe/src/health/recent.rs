//! Whether a past restart or pattern match still counts as current.
//! "Now" and the color of a namespace's rectangle on the map show only
//! what is happening, so a restart or a match counts toward them only
//! while it is recent. Older ones are still listed, in gray.

use crate::logs::patterns::seconds;

/// How many minutes a restart or a match stays recent. A pattern that
/// searches a shorter window uses that window instead, so its matches
/// leave "Now" when its search stops finding them.
pub const RECENT_MINUTES: i64 = 60;

/// How many minutes a match of a pattern that searches `window` stays
/// recent: `RECENT_MINUTES`, or the window when it is shorter, but at
/// least one minute, so a window of seconds still has a recent match.
pub fn span(window: &str) -> i64 {
    let window = seconds(window).map_or(RECENT_MINUTES, |seconds| seconds / 60);
    window.clamp(1, RECENT_MINUTES)
}

/// Whether a match `minutes_ago` minutes old, of a pattern that searches
/// `window`, is recent. A match exactly `span(window)` old is not.
pub fn is_recent(minutes_ago: i64, window: &str) -> bool {
    minutes_ago < span(window)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::just_now(0, "1h", true)]
    #[case::inside_the_hour(59, "1h", true)]
    #[case::at_the_hour(60, "1h", false)]
    #[case::a_day_window_still_ends_at_the_hour(60, "24h", false)]
    #[case::inside_the_hour_of_a_day_window(59, "24h", true)]
    #[case::inside_a_shorter_window(29, "30m", true)]
    #[case::at_a_shorter_windows_edge(30, "30m", false)]
    #[case::a_window_of_seconds(0, "30s", true)]
    #[case::past_a_window_of_seconds(1, "30s", false)]
    fn a_match_is_recent_inside_the_span(
        #[case] minutes_ago: i64,
        #[case] window: &str,
        #[case] recent: bool,
    ) {
        assert_eq!(is_recent(minutes_ago, window), recent);
    }
}
