//! Matches text against a template from kind knowledge, such as a
//! revision's `{ref}@sha1:{commit}` or an applier's mark `{name}:*`, and
//! returns what each `{placeholder}` matched.  A `{placeholder}` matches
//! as few characters as it can, and at least one.  `*` matches as many
//! as it can, and may match none.  So `{name}:*` captures the text
//! before the first `:`, and `{ref}@sha256:*` captures `a@b` from
//! `a@b@sha256:ff`, because `sha256:` must follow the `@`.

use std::collections::HashMap;

enum Token<'a> {
    Text(&'a str),
    /// A `{name}` placeholder, or `*`, which has no name.
    Placeholder(Option<&'a str>),
}

/// The text that each placeholder of `template` matches in `text`, or
/// `None` when `text` does not match the template.
pub fn captures<'a>(template: &str, text: &'a str) -> Option<HashMap<String, &'a str>> {
    let tokens = tokens(template);
    let mut found = HashMap::new();
    matches(&tokens, text, &mut found).then_some(found)
}

fn tokens(template: &str) -> Vec<Token<'_>> {
    let mut tokens = Vec::new();
    let mut rest = template;
    while !rest.is_empty() {
        let next = rest.find(['{', '*']).unwrap_or(rest.len());
        if next > 0 {
            tokens.push(Token::Text(&rest[..next]));
            rest = &rest[next..];
            continue;
        }
        if let Some(after) = rest.strip_prefix('*') {
            tokens.push(Token::Placeholder(None));
            rest = after;
            continue;
        }
        match rest[1..].split_once('}') {
            Some((name, after)) => {
                tokens.push(Token::Placeholder(Some(name)));
                rest = after;
            }
            None => {
                tokens.push(Token::Text(rest));
                rest = "";
            }
        }
    }
    tokens
}

fn matches<'a>(tokens: &[Token], text: &'a str, found: &mut HashMap<String, &'a str>) -> bool {
    let Some((token, rest)) = tokens.split_first() else {
        return text.is_empty();
    };
    match token {
        Token::Text(literal) => text
            .strip_prefix(literal)
            .is_some_and(|after| matches(rest, after, found)),
        Token::Placeholder(name) => {
            let ends = text.char_indices().map(|(at, _)| at).chain([text.len()]);
            let ends: Vec<usize> = match name {
                Some(_) => ends.filter(|end| *end > 0).collect(),
                None => ends.rev().collect(),
            };
            ends.into_iter().any(|end| {
                let taken = matches(rest, &text[end..], found);
                if let (true, Some(name)) = (taken, name) {
                    found.insert(name.to_string(), &text[..end]);
                }
                taken
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::a_ref_and_a_commit("{ref}@sha1:{commit}", "main@sha1:0a1b2c3", Some(vec![("commit", "0a1b2c3"), ("ref", "main")]))]
    #[case::a_bare_commit("sha1:{commit}", "sha1:0a1b2c3", Some(vec![("commit", "0a1b2c3")]))]
    #[case::a_star_after_the_first_at("{ref}@*", "a@b@sha256:ff", Some(vec![("ref", "a")]))]
    #[case::the_at_before_the_literal_text("{ref}@sha256:*", "a@b@sha256:ff", Some(vec![("ref", "a@b")]))]
    #[case::a_star_that_matches_nothing("{name}:*", "shop:", Some(vec![("name", "shop")]))]
    #[case::two_parts("{namespace}_{name}:*", "ops_shop:apps/Deployment:shop/web", Some(vec![("name", "shop"), ("namespace", "ops")]))]
    #[case::no_match("{ref}@sha1:{commit}", "1.4.0", None)]
    #[case::a_placeholder_that_matches_nothing("sha1:{commit}", "sha1:", None)]
    #[case::text_without_placeholders("main", "main", Some(vec![]))]
    fn captures_each_placeholder(
        #[case] template: &str,
        #[case] text: &str,
        #[case] expected: Option<Vec<(&str, &str)>>,
    ) {
        let found = captures(template, text).map(|found| {
            let mut found: Vec<(String, &str)> = found.into_iter().collect();
            found.sort();
            found
        });
        let expected = expected.map(|pairs| {
            let pairs = pairs.into_iter().map(|(k, v)| (k.to_string(), v));
            pairs.collect::<Vec<_>>()
        });
        assert_eq!(found, expected);
    }
}
