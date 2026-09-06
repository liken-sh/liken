// The fold every searchable string and every query text goes through:
// lowercase, diacritics removed, split on every character that is not a
// letter or a digit. Both sides go through this one function, because
// a query folded one way against strings folded another way misses
// exactly the matches a person expects.

use unicode_normalization::UnicodeNormalization;
use unicode_normalization::char::is_combining_mark;

/// The words a string folds to. Text with no letters or digits folds to
/// no words.
pub fn words(text: &str) -> Vec<String> {
    let mut found = Vec::new();
    fold(text, &mut found);
    found
}

/// The same fold, appended to a buffer the caller owns. A build folds
/// hundreds of thousands of strings, and reusing one buffer saves an
/// allocation per string.
pub fn fold(text: &str, found: &mut Vec<String>) {
    let mut word = String::new();
    // NFD splits "é" into "e" plus a combining accent, so dropping the
    // combining marks leaves the base letter. "ß" has no decomposition
    // and stays as it is.
    for letter in text.nfd() {
        if is_combining_mark(letter) {
            continue;
        }
        if letter.is_alphanumeric() {
            word.extend(letter.to_lowercase());
        } else if !word.is_empty() {
            found.push(std::mem::take(&mut word));
        }
    }
    if !word.is_empty() {
        found.push(word);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_string_folds_to_lowercase_words_split_on_everything_else() {
        assert_eq!(words("The Matrix"), ["the", "matrix"]);
        assert_eq!(
            words("Spider-Man: No Way Home"),
            ["spider", "man", "no", "way", "home"]
        );
        assert_eq!(words("2001"), ["2001"]);
        assert_eq!(words("  "), Vec::<String>::new());
        assert_eq!(words(""), Vec::<String>::new());
    }

    #[test]
    fn a_diacritic_folds_away() {
        assert_eq!(words("Amélie"), ["amelie"]);
        assert_eq!(words("Ñuñez"), ["nunez"]);
        assert_eq!(words("Straße"), ["straße"]);
    }

    #[test]
    fn a_fold_appends_to_the_buffer_it_is_given() {
        let mut found = vec!["kept".to_string()];
        fold("A Second", &mut found);
        assert_eq!(found, ["kept", "a", "second"]);
    }
}
