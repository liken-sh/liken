//! The objects a sentence names, linked.  A loss or a card writes its
//! lines as pieces, which keep the words apart from the objects they
//! name, and `Linker` makes each object with a page a link to it.

use crate::edges::Object;

/// One piece of what a page shows: a name with the page it opens, or
/// words between names, which open none.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Part {
    pub text: String,
    pub href: Option<String>,
}

impl Part {
    pub fn plain(text: &str) -> Self {
        Self {
            text: text.into(),
            href: None,
        }
    }
}

/// One piece of a sentence as a loss or a card writes it: words, an
/// object shown as the text given, or a page that is no object's, such
/// as a kind's.  The objects stay apart from the words, so a page links
/// each object a line names and never a word that looks like a name.
#[derive(Clone, Debug, PartialEq)]
pub enum Piece {
    Words(String),
    Name(Object, String),
    Page(String, String),
}

impl Piece {
    pub fn words(words: impl Into<String>) -> Self {
        Self::Words(words.into())
    }

    fn shown(&self) -> &str {
        match self {
            Self::Words(text) | Self::Name(_, text) | Self::Page(text, _) => text,
        }
    }
}

/// `pieces` as plain text.
pub fn text(pieces: &[Piece]) -> String {
    pieces.iter().map(Piece::shown).collect()
}

/// `items` joined as an English list: "a and b", "a, b, and c".
pub fn listed(items: Vec<Vec<Piece>>) -> Vec<Piece> {
    let count = items.len();
    let mut pieces = Vec::new();
    for (at, item) in items.into_iter().enumerate() {
        let joint = match (count, at) {
            (_, 0) => None,
            (2, _) => Some(" and "),
            (_, at) if at + 1 == count => Some(", and "),
            _ => Some(", "),
        };
        pieces.extend(joint.map(Piece::words));
        pieces.extend(item);
    }
    pieces
}

/// The objects a set of sentences may name, with their pages, so each
/// line can link the names it holds.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Linker {
    /// Each object with a page, and its page.
    pages: Vec<(Object, String)>,
}

impl Linker {
    /// The linker of `objects`, each with the page `href` gives it; an
    /// object with none is left out.
    pub fn new(
        objects: impl IntoIterator<Item = Object>,
        href: impl Fn(&Object) -> Option<String>,
    ) -> Self {
        let mut pages: Vec<(Object, String)> = objects
            .into_iter()
            .filter_map(|object| {
                let href = href(&object)?;
                Some((object, href))
            })
            .collect();
        pages.sort();
        pages.dedup();
        Self { pages }
    }

    /// The page of `object`, when it is one of the linker's.
    pub fn href(&self, object: &Object) -> Option<&str> {
        let found = self.pages.iter().find(|(o, _)| o == object);
        found.map(|(_, href)| href.as_str())
    }

    /// `pieces` as parts, each object they name linked to its page
    /// when it has one, and the words between run together.
    pub fn link(&self, pieces: &[Piece]) -> Vec<Part> {
        let mut parts: Vec<Part> = Vec::new();
        for piece in pieces {
            let part = match piece {
                Piece::Words(words) => Part::plain(words),
                Piece::Name(object, shown) => Part {
                    text: shown.clone(),
                    href: self.href(object).map(Into::into),
                },
                Piece::Page(shown, href) => Part {
                    text: shown.clone(),
                    href: Some(href.clone()),
                },
            };
            match parts.last_mut() {
                Some(last) if last.href.is_none() && part.href.is_none() => {
                    last.text.push_str(&part.text);
                }
                _ => parts.push(part),
            }
        }
        parts
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn parts(parts: &[(&str, Option<&str>)]) -> Vec<Part> {
        let parts = parts.iter().map(|(text, href)| Part {
            text: (*text).into(),
            href: href.map(Into::into),
        });
        parts.collect()
    }

    fn web() -> Object {
        Object::new("apps", "Deployment", Some("shop"), "web")
    }

    /// `object` shown by its name.
    fn name(object: &Object) -> Piece {
        Piece::Name(object.clone(), object.name.clone())
    }

    fn linker() -> Linker {
        let objects = [
            web(),
            Object::new("example.io", "Cache", Some("shop"), "cache"),
        ];
        Linker::new(objects, |object| {
            (object.kind != "Cache").then(|| format!("/{}", object.name))
        })
    }

    #[rstest]
    #[case::words_alone(vec![Piece::words("web is down")], &[("web is down", None)])]
    #[case::a_name(
        vec![name(&web()), Piece::words(" is down")],
        &[("web", Some("/web")), (" is down", None)]
    )]
    #[case::a_name_in_the_words_is_no_link(
        vec![name(&web()), Piece::words(" loses the web app")],
        &[("web", Some("/web")), (" loses the web app", None)]
    )]
    #[case::an_object_with_no_page(
        vec![name(&Object::new("example.io", "Cache", Some("shop"), "cache")), Piece::words(" stops")],
        &[("cache stops", None)]
    )]
    #[case::a_page(
        vec![Piece::words("Nothing reconciles "), Piece::Page("3 Caches".into(), "/kinds/caches".into())],
        &[("Nothing reconciles ", None), ("3 Caches", Some("/kinds/caches"))]
    )]
    fn links_the_objects_a_line_names(
        #[case] pieces: Vec<Piece>,
        #[case] expected: &[(&str, Option<&str>)],
    ) {
        assert_eq!(linker().link(&pieces), parts(expected));
    }

    #[rstest]
    #[case::one(1, "a")]
    #[case::two(2, "a and b")]
    #[case::three(3, "a, b, and c")]
    fn lists_its_items(#[case] count: usize, #[case] expected: &str) {
        let items = ["a", "b", "c"].map(|item| vec![Piece::words(item)]);
        assert_eq!(text(&listed(items[..count].to_vec())), expected);
    }
}
