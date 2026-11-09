// The cut of a block of text to a number of lines. A plot or a synopsis
// longer than the space a page gives it ends at a sentence's end where
// one falls in its last line, and otherwise after the last whole word
// that fits with an ellipsis, so the reader sees where the text stops and
// never reads half a word.
//
// The shaper measures every word of the block, and a page draws the same
// block on every frame. So each cut is kept in a small cache, keyed by
// the content, the size, the width, and the cap, and a frame that draws
// the block again reads the cut instead of shaping a long plot word by
// word.

use std::cell::RefCell;
use std::collections::HashMap;

use super::{measured, measured_cut, wrapped};

/// The content broken into at most `cap` lines of this width at this
/// size, from the shaper's own measure. Content longer than `cap` lines
/// ends its last line at a sentence's end where one falls in that line,
/// and otherwise after the last whole word that fits beside an ellipsis.
pub fn clamped(content: &str, size: f32, width: f32, cap: usize) -> Vec<String> {
    let mut lines = wrapped(content, size, width);
    if lines.len() <= cap {
        return lines;
    }
    lines.truncate(cap);
    if let Some(last) = lines.pop() {
        lines.push(ended(&last, size, width));
    }
    lines
}

// The last line of a block that goes on past it.
fn ended(line: &str, size: f32, width: f32) -> String {
    if let Some(end) = sentence_end(line) {
        return line[..end].to_string();
    }
    let mut words: Vec<&str> = line.split(' ').collect();
    while words.len() > 1 {
        let kept = ellipsed(&words.join(" "));
        if measured(&kept, size) <= width {
            return kept;
        }
        words.pop();
    }
    // One word fills the whole line, so the cut falls inside it.
    let kept = ellipsed(words[0]);
    match measured(&kept, size) <= width {
        true => kept,
        false => measured_cut(words[0], size, width),
    }
}

// The words kept and an ellipsis after them. A comma, a colon, or a dash
// at the end of the kept words would read as part of the ellipsis, so
// it is dropped.
fn ellipsed(kept: &str) -> String {
    let trimmed = kept.trim_end_matches([',', ';', ':', ' ', '-', '\u{2013}', '\u{2014}']);
    format!("{trimmed}\u{2026}")
}

// The byte offset just past the last sentence's end in this line: a
// full stop, a question mark, or an exclamation mark, with any closing
// quote or bracket after it, at the end of a word. A short capitalized
// word with a full stop, such as "Dr." or "St.", is a title or an
// abbreviation and ends no sentence.
fn sentence_end(line: &str) -> Option<usize> {
    let mut found = None;
    for word in words_with_ends(line) {
        let (start, end) = word;
        let token = &line[start..end];
        let bare = token.trim_end_matches(['"', '\'', ')', '\u{201d}', '\u{2019}']);
        let Some(last) = bare.chars().last() else {
            continue;
        };
        if matches!(last, '.' | '!' | '?') && !(last == '.' && abbreviated(bare)) {
            found = Some(end);
        }
    }
    found
}

// The start and end byte offsets of each word in a line of words that
// single spaces separate.
fn words_with_ends(line: &str) -> impl Iterator<Item = (usize, usize)> + '_ {
    let mut start = 0;
    line.split(' ').map(move |word| {
        let at = start;
        start += word.len() + 1;
        (at, at + word.len())
    })
}

// Whether a word that ends in a full stop is a title or an abbreviation:
// three letters or fewer before the stop, the first a capital.
fn abbreviated(word: &str) -> bool {
    let letters = word.trim_start_matches(['"', '\'', '(', '\u{201c}', '\u{2018}']);
    let stem = letters.trim_end_matches('.');
    let first = stem.chars().next();
    first.is_some_and(char::is_uppercase) && stem.chars().count() <= 3
}

// The most cuts the cache keeps. A screen draws a few blocks, and a page
// that opens draws its own, so a handful covers every frame.
const KEPT: usize = 32;

// What a cut is kept by: the content, the bits of the size and of the
// width, and the cap.
type Key = (String, u32, u32, usize);

// One cut: the lines joined by line breaks, and how many there are.
type Cut = (String, usize);

thread_local! {
    static CUTS: RefCell<HashMap<Key, Cut>> = RefCell::new(HashMap::new());
}

/// The block's lines joined by line breaks, and how many lines there
/// are, from the cache where this block was cut before. The cache is
/// emptied when it fills, because every block a frame draws is cut
/// again on the next frame and fills it again.
pub fn kept(content: &str, size: f32, width: f32, cap: usize) -> (String, usize) {
    let key = (content.to_string(), size.to_bits(), width.to_bits(), cap);
    CUTS.with(|cuts| {
        if let Some(cut) = cuts.borrow().get(&key) {
            return cut.clone();
        }
        let lines = clamped(content, size, width, cap);
        let cut = (lines.join("\n"), lines.len());
        let mut cuts = cuts.borrow_mut();
        if cuts.len() >= KEPT {
            cuts.clear();
        }
        cuts.insert(key, cut.clone());
        cut
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const SIZE: f32 = 24.0;

    // A plot of about six hundred characters with no sentence end until
    // its last word.
    fn plot() -> String {
        "a young woman insists that her father is trapped somewhere below the old \
         house on the hill and that the doctor who runs the hospital knows more \
         than he says about the box her father bought from a stranger in a market "
            .repeat(3)
            + "and she goes after him."
    }

    #[test]
    fn a_plot_longer_than_its_cap_ends_its_last_line_in_an_ellipsis_inside_the_width() {
        let plot = plot();
        assert!(plot.len() > 600);

        let lines = clamped(&plot, SIZE, 700.0, 4);

        assert_eq!(lines.len(), 4);
        let last = lines.last().expect("four lines");
        assert!(last.ends_with('\u{2026}'), "{last}");
        assert!(!last.ends_with(" \u{2026}"), "{last}");
        assert!(lines.iter().all(|line| measured(line, SIZE) <= 700.0));
    }

    #[test]
    fn the_ellipsis_follows_a_whole_word() {
        let plot = plot();
        let lines = clamped(&plot, SIZE, 700.0, 4);
        let last = lines
            .last()
            .expect("four lines")
            .trim_end_matches('\u{2026}');
        let word = last.rsplit(' ').next().expect("a word");

        assert!(
            plot.split_whitespace().any(|whole| whole == word),
            "{word} is a whole word of the plot"
        );
    }

    #[test]
    fn a_sentence_end_in_the_last_line_ends_the_block_there() {
        let plot = "The first sentence runs on for a while. The second one does too and keeps going \
                    well past the end of the line that holds it.";
        let lines = clamped(plot, SIZE, 520.0, 1);

        assert_eq!(
            lines,
            vec!["The first sentence runs on for a while.".to_string()]
        );
    }

    #[test]
    fn a_title_s_full_stop_ends_no_sentence() {
        assert_eq!(sentence_end("the thrill-seeking Dr. Channard and"), None);
        assert_eq!(sentence_end("a rescue. Kirsty and Dr."), Some(9));
        assert_eq!(sentence_end("she asks \"why?\" and"), Some(15));
    }

    #[test]
    fn a_plot_that_fits_keeps_every_word_and_no_ellipsis() {
        let lines = clamped("A short plot.", SIZE, 700.0, 4);
        assert_eq!(lines, vec!["A short plot.".to_string()]);
        assert!(clamped("", SIZE, 700.0, 4).is_empty());
    }

    #[test]
    fn a_comma_before_the_cut_is_dropped() {
        assert_eq!(ellipsed("in trying,"), "in trying\u{2026}");
        assert_eq!(ellipsed("and then \u{2014}"), "and then\u{2026}");
    }

    #[test]
    fn one_word_wider_than_the_line_is_cut_inside_it() {
        let word = "Supercalifragilisticexpialidocious".repeat(2);
        let lines = clamped(&format!("{word} {word}"), SIZE, 300.0, 1);

        assert!(lines[0].ends_with('\u{2026}'));
        assert!(measured(&lines[0], SIZE) <= 300.0);
    }

    #[test]
    fn a_block_drawn_again_reads_the_same_cut() {
        let plot = plot();
        let first = kept(&plot, SIZE, 700.0, 4);
        assert_eq!(first.1, 4);
        assert_eq!(first.0.lines().count(), 4);
        assert_eq!(kept(&plot, SIZE, 700.0, 4), first);
    }

    #[test]
    fn a_full_cache_starts_again() {
        for index in 0..=KEPT {
            kept(&format!("plot {index}"), SIZE, 700.0, 4);
        }
        CUTS.with(|cuts| assert!(cuts.borrow().len() <= KEPT));
    }
}
