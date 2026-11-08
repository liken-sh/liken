// The facts line under a person's name: the dates of the life, the
// credits and the years they span, and how many of them everyone at the
// screen has seen. `born 1970 · 31 credits from 1985 to now · you've seen 9`.

use super::credit::{Credit, current};
use super::seen;
use crate::screens::facts;

/// The facts line of these credits under these dates, at the year `now`,
/// for an audience of `people` people. The seen count is left out while
/// nobody is at the screen.
pub fn line(dates: &str, credits: &[Credit], now: i64, people: usize) -> String {
    let span = spanned(credits, now);
    let seen = match people {
        0 => String::new(),
        _ => format!("you've seen {}", seen::everyone(credits)),
    };
    facts::joined(&[dates, &span, &seen])
}

// The credits and the years they span, with the undated ones counted
// after them.
fn spanned(credits: &[Credit], now: i64) -> String {
    let years: Vec<i64> = credits.iter().filter_map(|credit| credit.year).collect();
    let undated = credits.len() - years.len();
    let dated = match (years.iter().min(), years.iter().max()) {
        (Some(&first), Some(&last)) => {
            let count = counted(years.len());
            match (current(last, now), first == last) {
                (true, _) => format!("{count} from {first} to now"),
                (false, true) => format!("{count} in {first}"),
                (false, false) => format!("{count} from {first} to {last}"),
            }
        }
        _ => String::new(),
    };
    match (dated.is_empty(), undated) {
        (_, 0) => dated,
        (true, 1) => "1 undated credit".to_string(),
        (true, undated) => format!("{undated} undated credits"),
        (false, undated) => format!("{dated}, and {undated} undated"),
    }
}

// A count of credits, one or more.
fn counted(count: usize) -> String {
    match count {
        1 => "1 credit".to_string(),
        count => format!("{count} credits"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::screens::person::credit::Seen;
    use crate::screens::person::tests::careers::{career, film, seen};

    const NOW: i64 = 2026;

    #[test]
    fn the_line_spans_the_credits_to_now_or_to_the_last() {
        let cases: [(&[i64], &str); 4] = [
            (
                &[1985, 2000, 2025],
                "born 1970 · 3 credits from 1985 to now",
            ),
            (&[1991, 2021], "born 1970 · 2 credits from 1991 to 2021"),
            (&[2003], "born 1970 · 1 credit in 2003"),
            (&[], "born 1970"),
        ];
        for (years, held) in cases {
            assert_eq!(line("born 1970", &career(years), NOW, 0), held, "{years:?}");
        }
    }

    #[test]
    fn undated_credits_count_after_the_dated_ones() {
        let mut credits = career(&[1990, 2025]);
        credits.push(film(8, None, Seen::Nobody));
        credits.push(film(9, None, Seen::Nobody));
        assert_eq!(
            line("", &credits, NOW, 0),
            "2 credits from 1990 to now, and 2 undated"
        );
        assert_eq!(
            line("", &[film(1, None, Seen::Nobody)], NOW, 0),
            "1 undated credit"
        );
    }

    #[test]
    fn the_seen_count_is_what_everyone_at_the_screen_has_seen() {
        let credits = seen(&[
            (1991, Seen::Everyone),
            (2001, Seen::Some),
            (2021, Seen::Everyone),
        ]);
        assert_eq!(
            line("1564 to 1616", &credits, NOW, 2),
            "1564 to 1616 · 3 credits from 1991 to 2021 · you've seen 2"
        );
        assert_eq!(
            line("1564 to 1616", &credits, NOW, 0),
            "1564 to 1616 · 3 credits from 1991 to 2021"
        );
    }
}
