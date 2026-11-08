// The dates of one person's life, and what they say about each credit:
// the age at the release, or how long after the death the work came
// out. The card under each poster and the death mark on the axis both
// read them here.

/// One date as the catalog holds it: a year, and the month and the day
/// where the value names them. Zero is a month or a day the value does
/// not name.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Day {
    pub year: i64,
    pub month: u32,
    pub day: u32,
}

/// The date a `born`, `died`, or `released` value holds: `1956-01-02`,
/// `1956-01`, or `1956`. Nothing for a value that starts with no year.
pub fn day(value: &str) -> Option<Day> {
    let mut fields = value.splitn(3, '-');
    let year = fields.next().filter(|year| year.len() == 4)?.parse().ok()?;
    let mut number = || fields.next().and_then(|field| field.get(..2)?.parse().ok());
    Some(Day {
        year,
        month: number().unwrap_or(0),
        day: number().unwrap_or(0),
    })
}

// The whole years from one date to a later one. With both dates whole,
// a birthday not yet reached in the later year takes one away. With a
// date that names only a year, the count is the difference of the years,
// because nothing says which side of the birthday the other date is on.
fn years(from: Day, to: Day) -> i64 {
    let whole = from.month > 0 && from.day > 0 && to.month > 0 && to.day > 0;
    let early = whole && (to.month, to.day) < (from.month, from.day);
    to.year - from.year - i64::from(early)
}

/// One life: the birth and the death the entry holds, and whether the
/// ages it gives are to be shown.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Life {
    pub born: Option<Day>,
    pub died: Option<Day>,
    // Whether the cards show ages. A birth date that puts any credit
    // before the birth is wrong for this person, so every age it gives is
    // suspect, and the page shows none of them.
    ages: bool,
}

impl Life {
    /// The life of an entry with these dates, credited in works released
    /// on these dates.
    pub fn of<'a>(born: &str, died: &str, released: impl IntoIterator<Item = &'a str>) -> Self {
        let born = day(born);
        let ages = born.is_some_and(|born| {
            released
                .into_iter()
                .filter_map(day)
                .all(|release| years(born, release) >= 0)
        });
        Self {
            born,
            died: day(died),
            ages,
        }
    }

    /// The person's age on this date, and nothing where the page shows no
    /// ages.
    pub fn age(&self, at: Day) -> Option<i64> {
        let born = self.born.filter(|_| self.ages)?;
        Some(years(born, at))
    }

    /// The second line of a credit's card: the year with the age at the
    /// release, the year with the time since the death for a work released
    /// in a later year, or the year alone where the page shows no ages.
    /// Nothing for a credit with no release date.
    pub fn line(&self, released: &str) -> String {
        let Some(release) = day(released) else {
            return String::new();
        };
        let year = release.year;
        if let Some(died) = self.died.filter(|died| year > died.year) {
            return match year - died.year {
                1 => format!("{year} · 1 year after death"),
                after => format!("{year} · {after} years after death"),
            };
        }
        match self.age(release) {
            Some(age) => format!("{year} · age {age}"),
            None => year.to_string(),
        }
    }

    /// The words of the death mark: the year, with the age at death where
    /// the page shows ages. Nothing for a person the entry gives no death.
    pub fn death(&self) -> Option<String> {
        let died = self.died?;
        Some(match self.age(died) {
            Some(age) => format!("died {}, age {age}", died.year),
            None => format!("died {}", died.year),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_date_reads_to_the_precision_the_value_holds() {
        let cases = [
            ("1956-01-02", Some((1956, 1, 2))),
            ("1956-07", Some((1956, 7, 0))),
            ("1956", Some((1956, 0, 0))),
            ("", None),
            ("unknown", None),
            ("56-01-02", None),
        ];
        for (value, held) in cases {
            let read = day(value).map(|day| (day.year, day.month, day.day));
            assert_eq!(read, held, "{value}");
        }
    }

    fn life(born: &str, died: &str) -> Life {
        Life::of(born, died, ["1990-01-01"])
    }

    #[test]
    fn a_card_reads_the_year_and_the_age_at_the_release() {
        let cases = [
            ("1970-06-01", "", "1986-05-02", "1986 · age 15"),
            ("1970-06-01", "", "1986-06-01", "1986 · age 16"),
            ("1970", "", "1986-05-02", "1986 · age 16"),
            ("1970-06-01", "", "1986", "1986 · age 16"),
            ("1986-03-01", "", "1986-09-01", "1986 · age 0"),
            ("", "", "2003-04-04", "2003"),
            ("", "", "", ""),
            ("1564", "1616", "1996", "1996 · 380 years after death"),
            ("1564", "1616", "1617", "1617 · 1 year after death"),
            ("1908", "1964", "1964-12-01", "1964 · age 56"),
        ];
        for (born, died, released, line) in cases {
            assert_eq!(life(born, died).line(released), line, "{born} {released}");
        }
    }

    // A credit released before the birth makes every age of the person
    // wrong, so no card shows one. The time after a death does not
    // depend on the birth, so it stays.
    #[test]
    fn a_birth_after_any_credit_takes_every_age_away() {
        let life = Life::of("1990", "2020", ["1985", "2000", "2024"]);
        assert_eq!(life.line("2000"), "2000");
        assert_eq!(life.line("2024"), "2024 · 4 years after death");
        assert_eq!(life.death(), Some("died 2020".to_string()));
    }

    #[test]
    fn the_death_mark_names_the_year_and_the_age_at_death() {
        assert_eq!(
            life("1908-03-02", "1964-09-30").death(),
            Some("died 1964, age 56".into())
        );
        assert_eq!(life("", "1616").death(), Some("died 1616".into()));
        assert_eq!(life("1908", "").death(), None);
    }
}
