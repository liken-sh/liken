// What the text of a frame says about titles. Two kinds of words tell the
// titles from a sign or a letter in the story: the name of a person the
// video credits, and a word that names a job. A name needs the list of the
// video's people. A job word needs no list, and in the experiments it told
// more than the names did, because the credits fact lists the actors, the
// directors, and the writers, and a credit roll is mostly crew.

// Text as lower-case ASCII letters with single spaces. The reader makes
// the same line differ by case, by an accent, and by punctuation from one
// frame to the next, and none of those matter to a match.
pub fn plain(text: &str) -> String {
    let mut plain = String::with_capacity(text.len());
    for character in text.chars().flat_map(char::to_lowercase) {
        let letter = match character {
            'a'..='z' => character,
            'à' | 'á' | 'â' | 'ã' | 'ä' | 'å' => 'a',
            'ç' => 'c',
            'è' | 'é' | 'ê' | 'ë' => 'e',
            'ì' | 'í' | 'î' | 'ï' => 'i',
            'ñ' => 'n',
            'ò' | 'ó' | 'ô' | 'õ' | 'ö' | 'ø' => 'o',
            'ù' | 'ú' | 'û' | 'ü' => 'u',
            'ý' | 'ÿ' => 'y',
            _ => ' ',
        };
        if letter != ' ' || !plain.ends_with(' ') {
            plain.push(letter);
        }
    }
    plain.trim().to_string()
}

// Words that name a job or a part of the credits. Each is in plain form,
// and a line holds one when the word is anywhere in it.
const ROLES: [&str; 38] = [
    "directed by",
    "written by",
    "produced by",
    "producer",
    "director of photography",
    "casting",
    "edited by",
    "editor",
    "music by",
    "music supervisor",
    "costume",
    "production design",
    "starring",
    "created by",
    "teleplay",
    "story by",
    "based on",
    "unit production manager",
    "assistant director",
    "stunt",
    "gaffer",
    "key grip",
    "best boy",
    "visual effects",
    "supervisor",
    "all rights reserved",
    "composer",
    "makeup",
    "wardrobe",
    "coordinator",
    "assistant",
    "copyright",
    "photography",
    "special effects",
    "sound mixer",
    "screenplay",
    "developed by",
    "production manager",
];

// How many of the job words are in any of the lines.
pub fn roles(lines: &[String]) -> usize {
    ROLES
        .iter()
        .filter(|role| lines.iter().any(|line| line.contains(**role)))
        .count()
}

// A name is worth a match when it has two words and at least this many
// letters. A short name or a single word is in ordinary dialogue and signs
// too often.
const FEWEST_LETTERS: usize = 8;

fn letters(text: &str) -> String {
    text.chars().filter(|character| *character != ' ').collect()
}

// The plain forms of the names that are specific enough to look for.
pub fn names(people: &[String]) -> Vec<String> {
    let mut names: Vec<String> = people
        .iter()
        .map(|name| plain(name))
        .filter(|name| name.contains(' ') && letters(name).len() >= FEWEST_LETTERS)
        .collect();
    names.sort();
    names.dedup();
    names
}

// How many of the names are in the text of one frame. A card of a title
// sequence often sets the first name over the last, each in a box of its
// own, and the reader drops the space of "EVA GREEN" in one frame and
// keeps it in the next. So a name is looked for in the letters of every
// line of the frame, run together with no spaces, in the order the reader
// gives the lines and with the name's words in either order, because two
// boxes at one height come in no fixed order.
pub fn named(lines: &[String], names: &[String]) -> usize {
    let text: String = lines.iter().map(|line| letters(line)).collect();
    names
        .iter()
        .filter(|name| {
            let turned: Vec<&str> = name.split(' ').rev().collect();
            holds(&text, &letters(name)) || holds(&text, &turned.concat())
        })
        .count()
}

// True when the line holds the name with at most one wrong letter in ten.
// The reader takes an l for an I and an E for an F, so an exact match
// misses names that a person reads at once. This is Sellers' algorithm: the edit distance from the name to the
// closest substring of the line.
fn holds(line: &str, name: &str) -> bool {
    let (line, name) = (line.as_bytes(), name.as_bytes());
    let allowed = name.len() / 10;
    if line.len() + allowed < name.len() {
        return false;
    }
    let mut row: Vec<usize> = vec![0; line.len() + 1];
    for (i, letter) in name.iter().enumerate() {
        let mut diagonal = row[0];
        row[0] = i + 1;
        for (j, other) in line.iter().enumerate() {
            let cost = diagonal + usize::from(letter != other);
            diagonal = row[j + 1];
            row[j + 1] = cost.min(row[j] + 1).min(diagonal + 1);
        }
    }
    row.iter()
        .min()
        .is_some_and(|distance| *distance <= allowed)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn lines(texts: &[&str]) -> Vec<String> {
        texts.iter().map(|text| plain(text)).collect()
    }

    #[test]
    fn plain_keeps_letters_and_folds_the_rest() {
        assert_eq!(plain("  Zoë  KRAVITZ, Jr. (2nd) "), "zoe kravitz jr nd");
    }

    #[test]
    fn roles_counts_each_job_word_once() {
        let read = lines(&[
            "Directed by SAM",
            "Executive Producers",
            "Co-Producer",
            "a street sign",
        ]);
        assert_eq!(roles(&read), 2);
    }

    #[test]
    fn names_drops_short_and_single_names() {
        let people = ["Eva Green", "Ade", "Madonna Louise", "Li Na", "Eva Green"].map(String::from);
        assert_eq!(names(&people), vec!["eva green", "madonna louise"]);
    }

    #[test]
    fn named_allows_one_wrong_letter_in_ten() {
        let known = names(&["Mads Mikkelsen".into(), "Eva Green".into()]);
        assert_eq!(named(&lines(&["Le Chiffre MADS MIKKELSFN"]), &known), 1);
        assert_eq!(named(&lines(&["Le Chiffre MADS MIKKFLSFN"]), &known), 0);
        assert_eq!(named(&lines(&["EVA GREEM"]), &known), 0);
        assert_eq!(
            named(&lines(&["Vesper Lynd EVA GREEN", "MADS MIKKELSEN"]), &known),
            2
        );
    }

    #[test]
    fn named_finds_a_name_set_over_two_lines_or_with_no_space() {
        let known = names(&["Chris Rock".into(), "Clyde Phillips".into()]);
        assert_eq!(named(&lines(&["CHRIS", "ROCK"]), &known), 1);
        assert_eq!(named(&lines(&["ROCK", "CHRIS"]), &known), 1);
        assert_eq!(named(&lines(&["DEVELOPED BY", "CLYDEPHILLIPS"]), &known), 1);
        assert_eq!(named(&lines(&["CHRIS", "a rock in the road"]), &known), 0);
    }
}
