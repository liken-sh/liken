// The words a screen makes of an item's columns: the year out of a
// release, the runtime out of a duration, and the one line those join
// into. Every function here is pure over the columns, so a screen builds
// its lines once at a read, and the tests need no window.

use crate::catalog::franchise::SERIES;

// The separator between two facts on one line.
const BETWEEN: &str = " · ";

/// The word a card reads a kind as. The catalog's series library and a
/// franchise file's series entry carry the same kind word; everything
/// else a card draws is a film.
pub fn kind_word(kind: &str) -> &'static str {
    match kind == SERIES {
        true => "Series",
        false => "Film",
    }
}

/// The year: the first four digits of a `released` column. A column that
/// holds neither a year nor a date answers nothing, which leaves the
/// year out of the line.
pub fn year(released: &str) -> &str {
    match released.get(..4) {
        Some(digits) if digits.chars().all(|digit| digit.is_ascii_digit()) => digits,
        _ => "",
    }
}

/// One date spelling for the whole browser: `September 22, 2004`,
/// `March 2027`, `2027`, by the precision the ISO value holds. A date
/// inside the months ahead a person reads as soon drops its year,
/// because the month and the day are what that person waits for. Every
/// date at or before today keeps its year, and a value that holds no
/// date reads as nothing.
pub fn date_worded(released: &str, today: &str) -> String {
    let year = year(released);
    if year.is_empty() {
        return String::new();
    }
    let mut parts = released
        .split('-')
        .skip(1)
        .map(|part| part.parse::<usize>().ok());
    let month = parts
        .next()
        .flatten()
        .filter(|month| (1..=12).contains(month));
    let day = parts.next().flatten().filter(|day| (1..=31).contains(day));
    let near = near(released, today);
    match (month, day) {
        (Some(month), Some(day)) => match near {
            true => format!("{} {day}", MONTHS[month - 1]),
            false => format!("{} {day}, {year}", MONTHS[month - 1]),
        },
        (Some(month), None) => match near {
            true => MONTHS[month - 1].to_string(),
            false => format!("{} {year}", MONTHS[month - 1]),
        },
        _ => year.to_string(),
    }
}

// How many calendar months ahead of today a date drops its year.
const NEAR_MONTHS: i64 = 9;

// Whether a date stands after today and no further ahead than the
// months a date drops its year inside.
fn near(released: &str, today: &str) -> bool {
    let today = parted(today);
    let released = parted(released);
    released > today && released <= horizon(today)
}

// Today, moved ahead by the months a near date runs to. A day the month
// it lands in does not hold stands past every date of that month, which
// is the answer the comparison wants.
fn horizon(today: (i64, i64, i64)) -> (i64, i64, i64) {
    let (year, month, day) = today;
    match month + NEAR_MONTHS > 12 {
        true => (year + 1, month + NEAR_MONTHS - 12, day),
        false => (year, month + NEAR_MONTHS, day),
    }
}

// The year, the month, and the day of an ISO value as numbers, with a
// part the value leaves out as zero, so two dates of different precision
// compare.
fn parted(iso: &str) -> (i64, i64, i64) {
    let mut parts = iso
        .split('-')
        .map(|part| part.parse::<i64>().unwrap_or_default());
    (
        parts.next().unwrap_or_default(),
        parts.next().unwrap_or_default(),
        parts.next().unwrap_or_default(),
    )
}

/// One count of one thing, spelled once for every screen: the count with
/// a comma every three digits, and the noun in the singular where the
/// count is one. `nouns` is the plural, the word the catalog's own
/// columns carry.
pub fn counted(count: i64, nouns: &str) -> String {
    let noun = match count {
        1 => singular(nouns),
        _ => nouns,
    };
    format!("{} {noun}", thousands(count))
}

// A noun that ends in "series" reads the same either way; every other
// noun a count carries drops its "s".
fn singular(nouns: &str) -> &str {
    match nouns.ends_with("series") {
        true => nouns,
        false => nouns.strip_suffix('s').unwrap_or(nouns),
    }
}

// A count as a person reads it, with a comma every three digits from
// the right.
fn thousands(count: i64) -> String {
    let digits = count.to_string();
    let mut read = String::with_capacity(digits.len() + digits.len() / 3);
    for (place, digit) in digits.char_indices() {
        if place > 0 && (digits.len() - place).is_multiple_of(3) {
            read.push(',');
        }
        read.push(digit);
    }
    read
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
    fn a_series_reads_as_a_series_and_every_other_kind_as_a_film() {
        assert_eq!(kind_word("series"), "Series");
        assert_eq!(kind_word("movies"), "Film");
        assert_eq!(kind_word("movie"), "Film");
        assert_eq!(kind_word(""), "Film");
    }

    // The day every date in these tests is read on.
    const TODAY: &str = "2026-09-05";

    #[test]
    fn a_date_reads_in_full_and_a_year_reads_alone() {
        let cases = [
            ("2004-09-22", "September 22, 2004"),
            ("1999-03-31", "March 31, 1999"),
            ("2004", "2004"),
            ("2004-13-01", "2004"),
            ("2004-09", "September 2004"),
            ("soon", ""),
            ("", ""),
        ];
        for (released, want) in cases {
            assert_eq!(date_worded(released, TODAY), want, "{released}");
        }
    }

    #[test]
    fn a_date_that_is_not_one_reads_as_the_year_it_holds() {
        let cases = [
            ("2027-13", "2027"),
            ("2027-xx-01", "2027"),
            ("2027-00", "2027"),
            ("", ""),
        ];
        for (released, want) in cases {
            assert_eq!(date_worded(released, TODAY), want, "{released}");
        }
    }

    #[test]
    fn a_date_inside_the_nine_months_ahead_drops_its_year() {
        let cases = [
            ("2027-03-12", "March 12"),
            ("2027-03", "March"),
            ("2026-09-06", "September 6"),
            ("2027-06-05", "June 5"),
            ("2027-06", "June"),
            ("2027", "2027"),
        ];
        for (released, want) in cases {
            assert_eq!(date_worded(released, TODAY), want, "{released}");
        }
    }

    #[test]
    fn a_date_past_the_nine_months_and_a_date_behind_today_keep_their_year() {
        let cases = [
            ("2027-06-06", "June 6, 2027"),
            ("2027-07", "July 2027"),
            ("2026-09-05", "September 5, 2026"),
            ("2026-09-04", "September 4, 2026"),
        ];
        for (released, want) in cases {
            assert_eq!(date_worded(released, TODAY), want, "{released}");
        }
    }

    #[test]
    fn nine_months_from_december_lands_in_the_next_year() {
        assert_eq!(date_worded("2027-06-30", "2026-12-31"), "June 30");
        assert_eq!(date_worded("2027-10-01", "2026-12-31"), "October 1, 2027");
    }

    #[test]
    fn a_count_reads_its_noun_in_the_singular_where_it_is_one() {
        let cases = [
            (1, "movies", "1 movie"),
            (1_422, "movies", "1,422 movies"),
            (1, "series", "1 series"),
            (165, "series", "165 series"),
            (1, "titles", "1 title"),
            (42, "titles", "42 titles"),
            (1, "films", "1 film"),
            (40, "films", "40 films"),
            (1, "episodes", "1 episode"),
            (30, "episodes", "30 episodes"),
            (1, "franchises", "1 franchise"),
            (32, "franchises", "32 franchises"),
            (40, "films and series", "40 films and series"),
        ];
        for (count, nouns, want) in cases {
            assert_eq!(counted(count, nouns), want, "{count} {nouns}");
        }
    }

    #[test]
    fn a_count_marks_its_thousands() {
        let cases = [
            (0, "0 films"),
            (999, "999 films"),
            (1_000, "1,000 films"),
            (12_345, "12,345 films"),
            (1_234_567, "1,234,567 films"),
        ];
        for (count, want) in cases {
            assert_eq!(counted(count, "films"), want, "{count}");
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
