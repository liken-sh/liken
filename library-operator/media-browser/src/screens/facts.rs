// The words a screen makes of an item's columns: the year out of a
// release, the runtime out of a duration, and the one line those join
// into. Every function here is pure over the columns, so a screen builds
// its lines once at a read, and the tests need no window.

// The separator between two facts on one line.
const BETWEEN: &str = " · ";

/// The year: the first four digits of a `released` column. A column that
/// holds neither a year nor a date answers nothing, which leaves the
/// year out of the line.
pub fn year(released: &str) -> &str {
    match released.get(..4) {
        Some(digits) if digits.chars().all(|digit| digit.is_ascii_digit()) => digits,
        _ => "",
    }
}

/// A duration in seconds as hours and minutes, or nothing where the
/// catalog holds none.
pub fn runtime(seconds: i64) -> String {
    let minutes = seconds / 60;
    if minutes <= 0 {
        return String::new();
    }
    match (minutes / 60, minutes % 60) {
        (0, minutes) => format!("{minutes}m"),
        (hours, 0) => format!("{hours}h"),
        (hours, minutes) => format!("{hours}h {minutes}m"),
    }
}

/// The facts that are present, joined into one line. A fact the row does
/// not carry leaves no gap and no separator behind.
pub fn joined(parts: &[&str]) -> String {
    let mut line = String::new();
    for part in parts.iter().filter(|part| !part.is_empty()) {
        if !line.is_empty() {
            line.push_str(BETWEEN);
        }
        line.push_str(part);
    }
    line
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_year_and_a_date_both_give_the_year() {
        assert_eq!(year("1999"), "1999");
        assert_eq!(year("2004-09-22"), "2004");
    }

    #[test]
    fn a_release_that_holds_no_year_gives_nothing() {
        assert_eq!(year(""), "");
        assert_eq!(year("soon"), "");
        assert_eq!(year("199"), "");
    }

    #[test]
    fn a_runtime_reads_as_hours_and_minutes() {
        assert_eq!(runtime(8_160), "2h 16m");
        assert_eq!(runtime(2_700), "45m");
        assert_eq!(runtime(7_200), "2h");
    }

    #[test]
    fn a_duration_under_a_minute_gives_nothing() {
        assert_eq!(runtime(0), "");
        assert_eq!(runtime(-1), "");
        assert_eq!(runtime(59), "");
    }

    #[test]
    fn the_line_carries_only_the_facts_that_are_there() {
        assert_eq!(joined(&["Specimen", "", "1h 37m", ""]), "Specimen · 1h 37m");
        assert_eq!(joined(&["", ""]), "");
        assert_eq!(joined(&["Specimen"]), "Specimen");
    }
}
