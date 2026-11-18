//! Ranks how well a name matches a search query, ignoring case. From
//! best to worst, the query is the whole name, a prefix of the name, a
//! prefix of a word in the name (words start after `-`, `.`, `/`, or
//! `_`), a substring of the name, or a set of letters that appear in
//! the name in order with other letters between them. Among those
//! scattered matches, a shorter span ranks higher.

/// How a name matches, best first.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Match {
    Exact,
    Prefix,
    WordPrefix,
    Substring,
    /// The query's letters in order, spread over this many bytes of
    /// the name from its first to its last.
    Subsequence(usize),
}

/// How `name` matches `query`, or `None` when it does not. `query` must
/// be lowercase.
pub fn rank(name: &str, query: &str) -> Option<Match> {
    let name = name.to_lowercase();
    if name == query {
        return Some(Match::Exact);
    }
    if name.starts_with(query) {
        return Some(Match::Prefix);
    }
    let mut words = name
        .match_indices(['-', '.', '/', '_'])
        .map(|(at, _)| at + 1);
    if words.any(|at| name[at..].starts_with(query)) {
        return Some(Match::WordPrefix);
    }
    if name.contains(query) {
        return Some(Match::Substring);
    }
    span(&name, query).map(Match::Subsequence)
}

/// The length in bytes of the shortest stretch of `name`, from its first
/// matched letter to its last, that contains the letters of `query` in
/// order. Returns `None` when `name` has no such stretch.
fn span(name: &str, query: &str) -> Option<usize> {
    let first = query.chars().next()?;
    let starts = name.match_indices(first).map(|(at, _)| at);
    starts
        .filter_map(|start| {
            let mut letters = name[start..].char_indices();
            let mut end = 0;
            for wanted in query.chars() {
                let (at, c) = letters.find(|(_, c)| *c == wanted)?;
                end = at + c.len_utf8();
            }
            Some(end)
        })
        .min()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::the_whole_name("ledger", "ledger", Some(Match::Exact))]
    #[case::any_case("Ledger", "ledger", Some(Match::Exact))]
    #[case::its_start("ledger-api", "led", Some(Match::Prefix))]
    #[case::a_word_after_a_dash("ledger-api", "api", Some(Match::WordPrefix))]
    #[case::a_word_after_a_dot("docs.example.test", "exam", Some(Match::WordPrefix))]
    #[case::a_word_after_a_slash("ghcr.io/acme/shop", "acme", Some(Match::WordPrefix))]
    #[case::a_word_after_an_underscore("night_backup", "back", Some(Match::WordPrefix))]
    #[case::letters_in_order("ledger-api", "lapi", Some(Match::Subsequence(10)))]
    #[case::the_tightest_letters("axxbaxb", "ab", Some(Match::Subsequence(3)))]
    #[case::letters_out_of_order("ledger", "dl", None)]
    #[case::inside_a_word("ledger", "dge", Some(Match::Substring))]
    #[case::no_match("ledger", "cache", None)]
    fn ranks_a_name(#[case] name: &str, #[case] query: &str, #[case] expected: Option<Match>) {
        assert_eq!(rank(name, query), expected);
    }

    #[rstest]
    #[case::a_substring_before_scattered_letters("ledger-api", "data-gateway", "dge")]
    #[case::tighter_letters_first("pg-dump", "pod-gateway-dump", "pgd")]
    fn ranks_one_name_above_another(
        #[case] better: &str,
        #[case] worse: &str,
        #[case] query: &str,
    ) {
        assert!(rank(better, query) < rank(worse, query));
    }
}
