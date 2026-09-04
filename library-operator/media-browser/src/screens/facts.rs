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

/// A release or air date as a person reads it, "September 22, 2004",
/// where the catalog holds a full date. A year alone reads as the year,
/// and anything else reads as nothing. A page header shows the full date
/// because the day a film came out or an episode aired is a fact a
/// person asks the header for, and a wall's caption keeps the year
/// because a caption has one line.
pub fn date(released: &str) -> String {
    let year = year(released);
    let mut parts = released
        .split('-')
        .skip(1)
        .map(|part| part.parse::<usize>().ok());
    match (parts.next().flatten(), parts.next().flatten()) {
        (Some(month), Some(day)) if (1..=12).contains(&month) && (1..=31).contains(&day) => {
            format!("{} {day}, {year}", MONTHS[month - 1])
        }
        _ => year.to_string(),
    }
}

const MONTHS: [&str; 12] = [
    "January",
    "February",
    "March",
    "April",
    "May",
    "June",
    "July",
    "August",
    "September",
    "October",
    "November",
    "December",
];

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
    Line::of(parts).words
}

/// One line of facts, with the end of each whole fact recorded, so a band
/// that cannot hold the whole line draws whole facts and never a dangling
/// separator.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Line {
    words: String,
    cuts: Vec<Cut>,
}

// Where one run of whole facts ends: the bytes it takes, and the
// characters the width estimate counts.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct Cut {
    bytes: usize,
    chars: usize,
}

impl Line {
    /// The facts that are present, joined, with a cut after each one.
    pub fn of(parts: &[&str]) -> Self {
        let mut words = String::new();
        let mut cuts = Vec::new();
        for part in parts.iter().filter(|part| !part.is_empty()) {
            if !words.is_empty() {
                words.push_str(BETWEEN);
            }
            words.push_str(part);
            cuts.push(Cut {
                bytes: words.len(),
                chars: words.chars().count(),
            });
        }
        Self { words, cuts }
    }

    /// The whole line, every fact it holds.
    pub fn words(&self) -> &str {
        &self.words
    }

    /// The longest run of whole facts that this many characters hold,
    /// dropping facts from the end. The first fact is the floor, and the
    /// band clips it where even that is longer.
    pub fn fitting(&self, chars: usize) -> &str {
        let cut = self.cuts.iter().rev().find(|cut| cut.chars <= chars);
        match cut.or(self.cuts.first()) {
            Some(cut) => &self.words[..cut.bytes],
            None => "",
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_date_reads_in_full_and_a_year_reads_alone() {
        let cases = [
            ("2004-09-22", "September 22, 2004"),
            ("1999-03-31", "March 31, 1999"),
            ("2004", "2004"),
            ("2004-13-01", "2004"),
            ("soon", ""),
            ("", ""),
        ];
        for (released, want) in cases {
            assert_eq!(date(released), want, "{released}");
        }
    }

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
    fn a_line_that_carries_no_facts_is_empty_at_any_width() {
        assert_eq!(Line::of(&["", ""]).words(), "");
        assert_eq!(Line::of(&["", ""]).fitting(40), "");
    }

    #[test]
    fn the_line_carries_only_the_facts_that_are_there() {
        assert_eq!(joined(&["Specimen", "", "1h 37m", ""]), "Specimen · 1h 37m");
        assert_eq!(joined(&["", ""]), "");
        assert_eq!(joined(&["Specimen"]), "Specimen");
    }
}
