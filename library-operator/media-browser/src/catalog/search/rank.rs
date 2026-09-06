// The two halves a hit is ranked by: where the match landed (a title,
// an alias, a person's name, an episode's title, a plot) and how it
// matched (the whole string, a prefix of the first word, a prefix of a
// later word, a substring inside a word). Where orders first, so the
// worst title match beats the best plot match.
//
// Two bytes carry this. The stored byte, one per link, says where the
// string came from and where the word stood in it, and is known at the
// build. The compared byte is computed at the read, once the query word
// and the indexed word are compared, so the index never stores a rank
// per possible query.

/// Where a string came from, best first. `Plot` and `EpisodePlot` share
/// one rung: a plot is a plot for the rank, and the split only marks
/// whether the string was an episode's, which decides the tie kind.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Where {
    Title,
    Alias,
    Person,
    EpisodeTitle,
    Plot,
    EpisodePlot,
}

impl Where {
    // The rung as a number, best first.
    fn rung(self) -> u8 {
        match self {
            Self::Title => 0,
            Self::Alias => 1,
            Self::Person => 2,
            Self::EpisodeTitle => 3,
            Self::Plot | Self::EpisodePlot => 4,
        }
    }

    // Whether the string was an episode's. A hit whose deciding match
    // came off an episode ties as an episode, after series and movies.
    fn episode(self) -> bool {
        matches!(self, Self::EpisodeTitle | Self::EpisodePlot)
    }
}

/// Where one word stands in the string it came from: the whole string,
/// the first word of several, or a later word.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Position {
    Alone,
    First,
    Later,
}

/// How a query word met an indexed word: equal, a prefix of it, or a
/// substring inside it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Relation {
    Equal,
    Prefix,
    Inner,
}

/// The kind order ties break by, best first: movies and series, then
/// sets and franchises, then episodes, then people.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, PartialOrd, Ord)]
pub enum Kind {
    #[default]
    Title,
    Collection,
    Episode,
    Person,
}

/// The byte the index stores for one word of one string: the rung in
/// the high bits, the position under it, and the episode mark in the
/// lowest bit. A smaller byte is a better match, so the build's plain
/// sort of links puts the best match of a word on an item first.
pub fn stored(rung: Where, position: Position) -> u8 {
    rung.rung() << 3 | (position as u8) << 1 | u8::from(rung.episode())
}

/// Whether the stored match came off an episode's string.
pub fn from_episode(stored: u8) -> bool {
    stored & 1 == 1
}

/// The rank a read compares hits by: the rung, then how the query word
/// met the indexed word. A prefix of a one-word string ranks as a
/// first-word match, because "bat" against "Batman" is the same kind
/// of hit as "bat" against "Batman Begins". The how depends on the
/// query, so it is computed here and never stored.
pub fn found(stored: u8, relation: Relation) -> u8 {
    let position = (stored >> 1) & 3;
    let how = match relation {
        Relation::Inner => 3,
        Relation::Equal if position == Position::Alone as u8 => 0,
        _ => position.max(Position::First as u8),
    };
    (stored >> 3) << 2 | how
}

/// How a query word meets an indexed word, or nothing when it does not.
pub fn relate(word: &str, asked: &str) -> Option<Relation> {
    if word == asked {
        return Some(Relation::Equal);
    }
    if word.starts_with(asked) {
        return Some(Relation::Prefix);
    }
    if word.contains(asked) {
        return Some(Relation::Inner);
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_query_word_meets_an_indexed_word_four_ways() {
        assert_eq!(relate("batman", "batman"), Some(Relation::Equal));
        assert_eq!(relate("batman", "bat"), Some(Relation::Prefix));
        assert_eq!(relate("batman", "atma"), Some(Relation::Inner));
        assert_eq!(relate("batman", "robin"), None);
    }

    // The rank of one match, as a read compares it.
    fn rank(rung: Where, position: Position, relation: Relation) -> u8 {
        found(stored(rung, position), relation)
    }

    #[test]
    fn the_whole_string_beats_a_first_word_beats_a_later_word_beats_an_inside() {
        let whole = rank(Where::Title, Position::Alone, Relation::Equal);
        let first = rank(Where::Title, Position::First, Relation::Equal);
        let later = rank(Where::Title, Position::Later, Relation::Equal);
        let inside = rank(Where::Title, Position::First, Relation::Inner);
        assert!(whole < first);
        assert!(first < later);
        assert!(later < inside);
    }

    #[test]
    fn a_prefix_of_a_string_of_one_word_is_a_first_word_match() {
        assert_eq!(
            rank(Where::Title, Position::Alone, Relation::Prefix),
            rank(Where::Title, Position::First, Relation::Equal)
        );
    }

    #[test]
    fn where_beats_how() {
        let worst_title = rank(Where::Title, Position::Later, Relation::Inner);
        let best_alias = rank(Where::Alias, Position::Alone, Relation::Equal);
        assert!(worst_title < best_alias);
    }

    #[test]
    fn the_five_rungs_rank_in_the_order_the_plan_names() {
        let rungs = [
            Where::Title,
            Where::Alias,
            Where::Person,
            Where::EpisodeTitle,
            Where::Plot,
        ];
        let ranks: Vec<u8> = rungs
            .iter()
            .map(|rung| rank(*rung, Position::Alone, Relation::Equal))
            .collect();
        assert!(ranks.windows(2).all(|pair| pair[0] < pair[1]));
        assert_eq!(
            rank(Where::EpisodePlot, Position::Alone, Relation::Equal),
            rank(Where::Plot, Position::Alone, Relation::Equal)
        );
    }

    #[test]
    fn only_an_episodes_strings_are_marked_as_one() {
        assert!(from_episode(stored(Where::EpisodeTitle, Position::Alone)));
        assert!(from_episode(stored(Where::EpisodePlot, Position::First)));
        assert!(!from_episode(stored(Where::Plot, Position::First)));
        assert!(!from_episode(stored(Where::Title, Position::Alone)));
    }

    #[test]
    fn the_better_of_two_matches_of_one_word_is_the_smaller_byte() {
        let title = stored(Where::Title, Position::Later);
        let plot = stored(Where::Plot, Position::Alone);
        assert!(title < plot);
        let alone = stored(Where::Title, Position::Alone);
        assert!(alone < title);
    }
}
