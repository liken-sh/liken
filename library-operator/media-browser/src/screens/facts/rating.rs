// The content rating as a card and a page draw it. An .nfo file's
// `mpaa` or `certification` element holds whatever the program that
// wrote it chose: `liken`'s enricher, Jellyfin, Sonarr, or a person.
// The same rating arrives as "TV-14", "TV14", "US:TV-14", or a bare
// "14", and "NR", "Not Rated", and "Unrated" all state that a title
// has no rating. The libraries are read-only by default, so the
// browser maps each spelling to one form here, at the draw, and leaves
// the files as they are.

/// The rating a work draws, from the rating its .nfo file holds. A US
/// film or TV rating in any spelling draws in the one form the MPA or
/// the TV Parental Guidelines print, and a spelling that states no
/// rating draws nothing. `series` says whether the work is a series,
/// because a bare "14" on a series is TV-14 and on a film it is not.
/// Every other value, such as another country's certificate, draws as
/// the file holds it: no US rating is a bare 12, 15, 16, or 18, so
/// such a value cannot be mistaken for one.
pub fn canonical(raw: &str, series: bool) -> String {
    let held = raw.trim();
    let key = compact(us_rating(held));
    if NONE.contains(&key.as_str()) {
        return String::new();
    }
    if let Some((_, form)) = US.iter().find(|(spelling, _)| *spelling == key) {
        return form.to_string();
    }
    match (series, key.as_str()) {
        (true, "14") => "TV-14".to_string(),
        _ => held.to_string(),
    }
}

// The rating without the prefix that marks it as American: Kodi writes
// "Rated PG-13", and a writer that names the country writes "US:PG-13".
fn us_rating(held: &str) -> &str {
    ["US:", "Rated "]
        .iter()
        .find_map(|prefix| {
            held.get(..prefix.len())
                .filter(|head| head.eq_ignore_ascii_case(prefix))
                .map(|_| held[prefix.len()..].trim())
        })
        .unwrap_or(held)
}

// A spelling with its case, its spaces, and its hyphens taken out, so
// "tv-14", "TV 14", and "TV14" are one key.
fn compact(spelling: &str) -> String {
    spelling
        .chars()
        .filter(|letter| !matches!(letter, ' ' | '-'))
        .map(|letter| letter.to_ascii_uppercase())
        .collect()
}

// The keys that state a title has no rating. OMDb answers "N/A" where
// it holds no rating at all.
const NONE: [&str; 5] = ["", "NR", "NOTRATED", "UNRATED", "N/A"];

// Each US rating's key and the form it draws in. TV-Y7-FV is TV-Y7 with
// the content descriptor for fantasy violence, and draws as TV-Y7, the
// rating it carries.
const US: [(&str, &str); 12] = [
    ("G", "G"),
    ("PG", "PG"),
    ("PG13", "PG-13"),
    ("R", "R"),
    ("NC17", "NC-17"),
    ("TVY", "TV-Y"),
    ("TVY7", "TV-Y7"),
    ("TVY7FV", "TV-Y7"),
    ("TVG", "TV-G"),
    ("TVPG", "TV-PG"),
    ("TV14", "TV-14"),
    ("TVMA", "TV-MA"),
];

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_rating_a_film_was_found_with_draws_in_one_form() {
        let cases = [
            ("R", "R"),
            ("PG-13", "PG-13"),
            ("PG", "PG"),
            ("NR", ""),
            ("G", "G"),
            ("", ""),
            ("Not Rated", ""),
            ("TV-MA", "TV-MA"),
            ("NC-17", "NC-17"),
            ("TV-14", "TV-14"),
            ("Unrated", ""),
        ];
        for (raw, want) in cases {
            assert_eq!(canonical(raw, false), want, "{raw}");
        }
    }

    #[test]
    fn every_rating_a_series_was_found_with_draws_in_one_form() {
        let cases = [
            ("TV-MA", "TV-MA"),
            ("TV-14", "TV-14"),
            ("TV-PG", "TV-PG"),
            ("", ""),
            ("TV-Y7", "TV-Y7"),
            ("16+", "16+"),
            ("TV-G", "TV-G"),
            ("TV-Y", "TV-Y"),
            ("12", "12"),
            ("14", "TV-14"),
            ("16", "16"),
            ("MA15+", "MA15+"),
            ("Not Rated", ""),
        ];
        for (raw, want) in cases {
            assert_eq!(canonical(raw, true), want, "{raw}");
        }
    }

    #[test]
    fn a_bare_number_on_a_series_is_a_tv_rating_only_where_one_matches() {
        let cases = [
            ("14", true, "TV-14"),
            ("14", false, "14"),
            ("12", true, "12"),
            ("15", true, "15"),
            ("16", true, "16"),
            ("18", true, "18"),
        ];
        for (raw, series, want) in cases {
            assert_eq!(canonical(raw, series), want, "{raw} {series}");
        }
    }

    #[test]
    fn another_spelling_of_a_us_rating_draws_in_its_one_form() {
        let cases = [
            ("TV14", "TV-14"),
            ("TV 14", "TV-14"),
            ("tv-ma", "TV-MA"),
            ("US:TV-14", "TV-14"),
            ("us:PG-13", "PG-13"),
            ("Rated R", "R"),
            ("Rated PG-13", "PG-13"),
            ("pg13", "PG-13"),
            ("NC17", "NC-17"),
            ("TV-Y7-FV", "TV-Y7"),
            (" PG ", "PG"),
        ];
        for (raw, want) in cases {
            assert_eq!(canonical(raw, false), want, "{raw}");
        }
    }

    #[test]
    fn every_spelling_of_no_rating_draws_nothing() {
        let cases = [
            "NR",
            "Not Rated",
            "not rated",
            "Unrated",
            "N/A",
            "US:NR",
            "Rated NR",
            "  ",
        ];
        for raw in cases {
            assert_eq!(canonical(raw, false), "", "{raw}");
            assert_eq!(canonical(raw, true), "", "{raw}");
        }
    }

    #[test]
    fn another_countrys_certificate_draws_as_the_file_holds_it() {
        let cases = [
            ("DE:16", "DE:16"),
            ("GB:15", "GB:15"),
            ("MA15+", "MA15+"),
            ("Approved", "Approved"),
        ];
        for (raw, want) in cases {
            assert_eq!(canonical(raw, true), want, "{raw}");
        }
    }
}
