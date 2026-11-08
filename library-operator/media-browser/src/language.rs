// Language tags as files and preferences write them, and the names a page
// shows for them. A Matroska track writes `eng` or `ger`, a subtitle file
// name writes `en` or `pt-BR`, and a `MediaPreferences` writes whatever its
// author typed. The subtitle rule and the audio choice compare tags by
// their base language, and a page names each language once in English.

mod codes;

use codes::{BIBLIOGRAPHIC, CODES};

/// The base language of a tag, in one form for each language: `en` for
/// `en`, `eng`, `EN`, and `en-US`, and `de` for `de`, `deu`, and `ger`. A
/// language with no two-letter code keeps its three-letter one, such as
/// `fil`. A tag that names no language, such as `und`, an empty string, a
/// malformed tag, or a word that is not a code, has none, and the answer is
/// empty.
///
/// This is media-operator's `canonicalLanguage`, which reads the
/// golang.org/x/text tables. Those tables hold every ISO 639-3 code, and
/// this table holds the ISO 639-1 languages alone, so a well-formed
/// three-letter code that no standard assigns, such as `xyz`, names a
/// language here and none in media-operator.
pub fn canonical(tag: &str) -> String {
    let mut subtags = tag.split(['-', '_']);
    let primary = subtags.next().unwrap_or_default().to_ascii_lowercase();
    let well_formed = subtags.all(|subtag| {
        (1..=8).contains(&subtag.len()) && subtag.chars().all(|c| c.is_ascii_alphanumeric())
    });
    if !well_formed || !primary.chars().all(|c| c.is_ascii_alphabetic()) {
        return String::new();
    }
    let two = |code: &str| code.to_string();
    match primary.len() {
        2 => CODES
            .iter()
            .find(|(code, _, _)| *code == primary)
            .map_or_else(String::new, |(code, _, _)| two(code)),
        3 if primary == "und" => String::new(),
        3 => CODES
            .iter()
            .find(|(_, terminology, _)| *terminology == primary)
            .map(|(code, _, _)| two(code))
            .or_else(|| {
                BIBLIOGRAPHIC
                    .iter()
                    .find(|(bibliographic, _)| *bibliographic == primary)
                    .map(|(_, code)| two(code))
            })
            .unwrap_or(primary),
        _ => String::new(),
    }
}

/// Whether two tags name the same base language. A tag that names no
/// language matches nothing, itself included.
pub fn same(a: &str, b: &str) -> bool {
    let canonical_a = canonical(a);
    !canonical_a.is_empty() && canonical_a == canonical(b)
}

/// The English name of a tag's language. A tag this table does not name
/// draws as the file carries it, because a tag a person can read is better
/// than nothing at all.
pub fn name(tag: &str) -> &str {
    let base = canonical(tag);
    CODES
        .iter()
        .find(|(code, _, _)| *code == base)
        .map_or(tag, |(_, _, name)| *name)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_form_of_a_language_reads_as_its_two_letter_code() {
        let cases = [
            ("en", "en"),
            ("eng", "en"),
            ("EN", "en"),
            ("en-US", "en"),
            ("en_GB", "en"),
            ("ger", "de"),
            ("deu", "de"),
            ("pt-BR", "pt"),
            ("zh-Hant-TW", "zh"),
            ("fil", "fil"),
            ("und", ""),
            ("", ""),
            ("qq", ""),
            ("english", ""),
            ("e1", ""),
            ("en-toolongsubtag", ""),
        ];
        for (tag, base) in cases {
            assert_eq!(canonical(tag), base, "{tag}");
        }
    }

    #[test]
    fn a_language_has_one_english_name_whatever_its_tag() {
        let cases = [
            ("en", "English"),
            ("eng", "English"),
            ("fre", "French"),
            ("no", "Norwegian"),
            ("qq", "qq"),
            ("fil", "fil"),
        ];
        for (tag, named) in cases {
            assert_eq!(name(tag), named, "{tag}");
        }
    }
}
