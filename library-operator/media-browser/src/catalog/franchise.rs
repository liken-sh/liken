// A franchise is one story across films and series in story order, read from a
// franchise.yaml in a git repository that a `Library` of kind franchises
// names. This module holds what the two reads of it answer with: the franchise
// a page draws, and the membership a strip draws. The words the page draws
// around these rows are the screen's own.

use super::{Answer, Slot, Title};

/// The franchise's own clock, from the file's calendar block. `unit` is years
/// or days. `zero` names the event the times count from, and the caption over
/// the page's time column reads it. `before` and `after` are the marks a
/// negative and a positive time take, BBY and ABY for Star Wars, and both are
/// empty for a calendar that counts in plain years or in days.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Calendar {
    pub unit: String,
    pub zero: String,
    pub before: String,
    pub after: String,
}

impl Calendar {
    /// The time label of one span, in the file's own marks. One time reads as
    /// "32 BBY", and a span as "22 to 20 BBY". A span that crosses zero
    /// carries a mark at each end, "5 BBY to 5 ABY". A calendar of years with
    /// no marks reads as plain years, "2024" and "-58", and one of days
    /// counts days from its zero, "Day 1141".
    pub fn label(&self, from: f64, to: f64) -> String {
        let (first, last) = (self.mark(from), self.mark(to));
        let span = match (from == to, first == last) {
            (true, _) => marked(&self.number(from), first),
            (false, true) => marked(
                &format!("{}{SPAN}{}", self.number(from), self.number(to)),
                last,
            ),
            (false, false) => format!(
                "{}{SPAN}{}",
                marked(&self.number(from), first),
                marked(&self.number(to), last)
            ),
        };
        self.counted(&span)
    }

    /// The caption over the times, "Years from the Battle of Yavin", and
    /// nothing where the file names no zero. The times on the page count from
    /// one event, and the caption is where the page says which event and in
    /// what unit.
    pub fn caption(&self) -> String {
        match self.zero.is_empty() {
            true => String::new(),
            false => format!("{} from {}", capitalized(&self.unit), lowered(&self.zero)),
        }
    }

    // The mark one time takes: the one for before zero on a negative time,
    // and the one for after zero on zero and every time past it.
    fn mark(&self, value: f64) -> &str {
        match value < 0.0 {
            true => &self.before,
            false => &self.after,
        }
    }

    // One time as the label writes it: the magnitude where a mark follows it,
    // because the mark says which side of zero the time is on and a minus
    // sign in front of BBY says it a second time. A time with no mark keeps
    // its sign, which is then all the reader has.
    fn number(&self, value: f64) -> String {
        match self.mark(value).is_empty() {
            true => number(value),
            false => number(value.abs()),
        }
    }

    // One label with the word a count of days leads with. A calendar of days
    // with no marks numbers its rows from zero, and 1141 alone reads as a
    // year. A calendar that names marks says what its numbers are already.
    fn counted(&self, label: &str) -> String {
        let bare = self.before.is_empty() && self.after.is_empty();
        match bare && self.unit == DAYS {
            true => format!("{DAY} {label}"),
            false => label.to_string(),
        }
    }
}

/// The word between the two times of a span. The page's time column splits a
/// label on it to stack the span on two lines, so the label and the column
/// spell the word once.
pub const SPAN: &str = " to ";

// The unit a calendar of days names, and the word its labels lead with.
const DAYS: &str = "days";
const DAY: &str = "Day";

// One time and the mark after it, and the time alone on a calendar that names
// no mark for that side of zero.
fn marked(value: &str, mark: &str) -> String {
    match mark.is_empty() {
        true => value.to_string(),
        false => format!("{value} {mark}"),
    }
}

// One time as the label writes it: the digits alone where the file gave a
// whole number, because a year is a whole number and a trailing zero after
// the point reads as a measurement.
fn number(value: f64) -> String {
    match value.fract() == 0.0 {
        true => format!("{value:.0}"),
        false => format!("{value}"),
    }
}

// The unit at the head of the caption, with its first letter in the upper
// case, because the caption starts with it.
fn capitalized(unit: &str) -> String {
    let mut letters = unit.chars();
    match letters.next() {
        Some(first) => first.to_uppercase().chain(letters).collect(),
        None => String::new(),
    }
}

// The zero as the caption reads it: a leading "The" in the lower case,
// because the file writes the zero as a title and the caption reads it inside
// a sentence.
fn lowered(zero: &str) -> String {
    match zero.strip_prefix("The ") {
        Some(rest) => format!("the {rest}"),
        None => zero.to_string(),
    }
}

/// One named stretch of the franchise's timeline. The file writes it, and the
/// page derives the rows it covers from each row's own span. Spans overlap on
/// purpose: a saga holds phases.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Era {
    pub name: String,
    pub from: f64,
    pub to: f64,
}

impl Era {
    /// How much of the timeline the era covers. The rail sorts on it, widest
    /// first, so a phase nests inside its saga.
    pub fn width(&self) -> f64 {
        self.to - self.from
    }

    /// Whether this era covers another one whole.
    pub fn holds(&self, inner: &Self) -> bool {
        self.from <= inner.from && inner.to <= self.to
    }

    /// Whether one span falls inside the era's own.
    pub fn meets(&self, from: f64, to: f64) -> bool {
        self.from <= to && from <= self.to
    }
}

/// The item some library of the namespace holds for one entry. The read keeps
/// the first library by name where two hold the member. `kind` is the item
/// table the row came from, movies or series. `arts` is every art file
/// beside the item, as the catalog's own list; a wall of landscape cells
/// picks its art out of that list, and a strip of posters draws `art` and
/// `tagline` and `plot` come out of the item's body, the way the movie
/// page reads them. A card of the wall draws the tagline, or the first
/// lines of the plot where there is none; a card of the strip leads with
/// the tagline and reads no plot.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Held {
    pub library: String,
    pub id: String,
    pub kind: String,
    pub title: String,
    pub art: String,
    pub arts: Vec<String>,
    pub released: String,
    pub slug: String,
    pub tagline: String,
    pub plot: String,
    /// The item's running time in seconds, and 0 where the catalog holds
    /// none. A film's card words it the way the film's own page does.
    pub duration: i64,
}

/// One entry of one franchise's order: a film, or one run of a series.
/// `position` is its place in story order, first to last. `kind` is movie or
/// series, as the file wrote it. `alias` is the provider alias the member's
/// own library writes. `title` is the file's own name for the member, drawn
/// only where no library holds it. `released` is the file's release date
/// at year, month, or day precision, empty where it gives none; the
/// standing of a gap reads it. `release_year` is derived from it, for
/// the year beside a title.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Entry {
    pub position: i64,
    pub kind: String,
    pub alias: String,
    pub title: String,
    pub released: String,
    pub release_year: i64,
    pub timed: bool,
    pub from: f64,
    pub to: f64,
    pub universes: Vec<String>,
    pub held: Option<Held>,
    pub episodes: i64,
}

// The two kinds a franchise entry names.
pub const MOVIE: &str = "movie";
pub const SERIES: &str = "series";

impl Entry {
    /// The name the page draws: the held item's own, and the file's title for
    /// a gap.
    pub fn name(&self) -> &str {
        match &self.held {
            Some(held) => &held.title,
            None => &self.title,
        }
    }

    /// Whether some library holds the entry, the title is still to come, or
    /// nobody has it yet. A gap is coming where `today`, an ISO date, cut
    /// to the precision of the file's release date, sorts before that
    /// date: `2027` is coming through 2026, `2026-10` through September
    /// 2026, and `2026-09-20` through the 19th. ISO dates at one precision
    /// sort as text, so the comparison is one string compare after the
    /// cut. A gap with no date is missing.
    pub fn standing(&self, today: &str) -> Standing {
        if self.held.is_some() {
            return Standing::Held;
        }
        let known = self.released.len().min(today.len());
        match !self.released.is_empty() && today[..known] < self.released[..] {
            true => Standing::Coming,
            false => Standing::Missing,
        }
    }
}

/// What the page says about an entry: the catalog holds it, the title is still
/// to come, or nobody has it yet. Held is the default, because it is the
/// ordinary case.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Standing {
    #[default]
    Held,
    Coming,
    Missing,
}

/// One franchise as its own page draws it: the header from the franchises row,
/// and every entry in story order, held or not. `universe` is the franchise's
/// own, the first column of the page. `calendar` is nothing where the file
/// names none, and the page then draws no time and no rail.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Franchise {
    pub library: String,
    pub id: String,
    pub title: String,
    pub art: String,
    pub universe: String,
    pub calendar: Option<Calendar>,
    pub eras: Vec<Era>,
    pub entries: Vec<Entry>,
}

/// The held members of one franchise as the slots of a strip, in story order.
/// Each slot carries the library and the kind of the item that holds it,
/// because a franchise spans libraries and both kinds. The answer names the
/// franchise, which is the strip's heading.
pub fn answer(franchise: Option<Franchise>) -> Answer {
    let Some(franchise) = franchise else {
        return Answer::default();
    };
    Answer {
        name: franchise.title,
        slots: slots(&franchise.entries),
    }
}

/// The held members of one order as the slots of a strip, in the order
/// they were given. A gap yields no slot, because a strip draws what a
/// person can play. The content rating is in neither read, so a slot
/// carries none.
pub fn slots(entries: &[Entry]) -> Vec<Slot> {
    entries
        .iter()
        .filter_map(|entry| entry.held.clone().map(slot))
        .collect()
}

/// One held member as a slot: what a strip draws, and what a select on it
/// opens.
pub fn slot(held: Held) -> Slot {
    Slot::of(
        &held.library,
        &held.kind,
        Title {
            id: held.id,
            title: held.title,
            released: held.released,
            art: held.art,
            duration: held.duration,
            rating: String::new(),
            tagline: held.tagline,
        },
    )
}

/// One franchise a title belongs to, with the members some library holds, in
/// story order. The strip on the title's page draws it, so a person plays what
/// the namespace has and the page behind the heading holds the rest. `movies`
/// and `series` count every entry of the order, held or not, and the strip's
/// heading carries that count, because the scope of the order is what tells
/// a franchise from a set.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Membership {
    pub library: String,
    pub id: String,
    pub title: String,
    pub movies: i64,
    pub series: i64,
    pub members: Vec<Entry>,
}

#[cfg(test)]
mod tests {
    use super::*;

    fn yavin() -> Calendar {
        Calendar {
            unit: "years".into(),
            zero: "the Battle of Yavin".into(),
            before: "BBY".into(),
            after: "ABY".into(),
        }
    }

    fn plain() -> Calendar {
        Calendar {
            unit: "years".into(),
            ..Calendar::default()
        }
    }

    fn outbreak() -> Calendar {
        Calendar {
            unit: "days".into(),
            zero: "The outbreak".into(),
            ..Calendar::default()
        }
    }

    #[test]
    fn one_time_reads_as_the_number_and_its_mark() {
        assert_eq!(yavin().label(-32.0, -32.0), "32 BBY");
        assert_eq!(yavin().label(5.0, 5.0), "5 ABY");
        assert_eq!(yavin().label(0.0, 0.0), "0 ABY");
        assert_eq!(plain().label(2024.0, 2024.0), "2024");
    }

    #[test]
    fn a_time_before_zero_drops_its_sign_where_a_mark_says_the_side() {
        assert_eq!(yavin().label(-32.0, -32.0), "32 BBY");
        assert_eq!(
            Calendar {
                after: String::new(),
                ..yavin()
            }
            .label(5.0, 5.0),
            "5"
        );
        assert_eq!(plain().label(-58.0, -58.0), "-58");
    }

    #[test]
    fn a_span_reads_as_two_numbers_and_one_mark() {
        assert_eq!(yavin().label(-22.0, -20.0), "22 to 20 BBY");
        assert_eq!(plain().label(2002.0, 2005.0), "2002 to 2005");
    }

    #[test]
    fn a_span_that_crosses_zero_carries_a_mark_at_each_end() {
        assert_eq!(yavin().label(-5.0, 5.0), "5 BBY to 5 ABY");
    }

    #[test]
    fn a_calendar_of_days_with_no_marks_counts_days() {
        let cases = [
            ((1141.0, 1141.0), "Day 1141"),
            ((1141.0, 1142.0), "Day 1141 to 1142"),
            ((-3.0, -3.0), "Day -3"),
            ((0.0, 0.0), "Day 0"),
        ];
        for ((from, to), label) in cases {
            assert_eq!(outbreak().label(from, to), label, "{from} to {to}");
        }
    }

    #[test]
    fn a_calendar_of_days_that_names_marks_leads_with_the_mark_and_not_the_day() {
        let dated = Calendar {
            unit: "days".into(),
            before: "BO".into(),
            after: "AO".into(),
            ..outbreak()
        };
        assert_eq!(dated.label(-3.0, -3.0), "3 BO");
        assert_eq!(dated.label(1141.0, 1141.0), "1141 AO");
    }

    #[test]
    fn a_time_between_two_years_keeps_its_point() {
        assert_eq!(plain().label(2024.5, 2024.5), "2024.5");
        assert_eq!(yavin().label(-32.5, -32.5), "32.5 BBY");
    }

    #[test]
    fn the_caption_names_the_unit_and_the_event_the_times_count_from() {
        let cases = [
            (yavin(), "Years from the Battle of Yavin"),
            (outbreak(), "Days from the outbreak"),
            (
                Calendar {
                    unit: "years".into(),
                    zero: "The Fall of the Twelve Colonies".into(),
                    ..Calendar::default()
                },
                "Years from the Fall of the Twelve Colonies",
            ),
            (
                Calendar {
                    zero: "Aegon's Conquest".into(),
                    ..plain()
                },
                "Years from Aegon's Conquest",
            ),
        ];
        for (calendar, caption) in cases {
            assert_eq!(calendar.caption(), caption, "{calendar:?}");
        }
    }

    #[test]
    fn a_calendar_with_no_zero_carries_no_caption() {
        assert_eq!(plain().caption(), "");
        assert_eq!(Calendar::default().caption(), "");
    }

    #[test]
    fn an_era_measures_and_nests_by_its_span() {
        let saga = Era {
            name: "The Saga".into(),
            from: -40.0,
            to: 40.0,
        };
        let phase = Era {
            name: "One Phase".into(),
            from: -5.0,
            to: 5.0,
        };
        assert_eq!(saga.width(), 80.0);
        assert!(saga.holds(&phase));
        assert!(!phase.holds(&saga));
    }

    #[test]
    fn an_era_meets_the_spans_that_touch_it() {
        let era = Era {
            name: "An Era".into(),
            from: -5.0,
            to: 5.0,
        };
        assert!(era.meets(0.0, 0.0));
        assert!(era.meets(-40.0, -5.0));
        assert!(era.meets(5.0, 40.0));
        assert!(!era.meets(6.0, 40.0));
        assert!(!era.meets(-40.0, -6.0));
    }

    fn film(title: &str) -> Entry {
        Entry {
            position: 1,
            kind: MOVIE.into(),
            alias: "movie:tmdb:1893".into(),
            title: title.into(),
            ..Entry::default()
        }
    }

    #[test]
    fn a_held_entry_draws_the_librarys_own_title_and_a_gap_the_files() {
        let entry = Entry {
            held: Some(Held {
                title: "The Held Title".into(),
                ..Held::default()
            }),
            ..film("The File's Title")
        };
        assert_eq!(entry.name(), "The Held Title");
        assert_eq!(entry.standing("2026-09-04"), Standing::Held);
        assert_eq!(film("A Film").name(), "A Film");
    }

    #[test]
    fn a_gap_is_coming_while_today_is_before_its_release_at_the_precision_given() {
        let released = |date: &str| Entry {
            released: date.into(),
            ..film("A Film")
        };
        let cases = [
            ("2031", "2026-09-04", Standing::Coming),
            ("2027", "2026-12-31", Standing::Coming),
            ("2026", "2026-09-04", Standing::Missing),
            ("1979", "2026-09-04", Standing::Missing),
            ("2026-10", "2026-09-04", Standing::Coming),
            ("2026-09", "2026-09-04", Standing::Missing),
            ("2026-09-20", "2026-09-04", Standing::Coming),
            ("2026-09-20", "2026-09-19", Standing::Coming),
            ("2026-09-20", "2026-09-20", Standing::Missing),
            ("2026-09-04", "2026-09-05", Standing::Missing),
        ];
        for (date, today, standing) in cases {
            assert_eq!(
                released(date).standing(today),
                standing,
                "{date} on {today}"
            );
        }
    }

    #[test]
    fn a_release_date_of_any_bytes_never_splits_the_day_it_is_held_against() {
        let cases = ["２０２７", "２７", "2027-０3", "soon™", "é"];
        for date in cases {
            let entry = Entry {
                released: date.into(),
                ..film("A Film")
            };
            let _ = entry.standing("2026-09-04");
        }
    }

    #[test]
    fn a_held_entry_stands_held_whatever_its_release() {
        let entry = Entry {
            released: "2099-01-01".into(),
            held: Some(Held::default()),
            ..film("A Film")
        };
        assert_eq!(entry.standing("2026-09-04"), Standing::Held);
    }

    #[test]
    fn a_gap_the_file_gives_no_release_date_is_missing() {
        assert_eq!(film("A Film").release_year, 0);
        assert_eq!(film("A Film").released, "");
        assert_eq!(film("A Film").standing("2026-09-04"), Standing::Missing);
    }
}
