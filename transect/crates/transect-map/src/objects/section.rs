//! What an object's board shows around the object: sections of facts,
//! each a label and the objects or words it names, and a table where
//! the facts have columns, as a Role's rules do.  Each section names
//! the side of the board it is drawn on, and every section, whatever its
//! kind, is drawn by one template from these.

use transect_graph::links::Reference;
use transect_resources::resources::OffLine;

/// The most names a fact lists before it folds to a count that opens
/// to them.
const MOST: usize = 6;

#[derive(Clone, Debug, PartialEq)]
pub struct Section {
    pub title: String,
    pub side: Side,
    pub facts: Vec<Fact>,
    pub table: Option<Table>,
    /// The line the section shows when it has nothing else.
    pub none: Option<OffLine>,
    /// The summary the section shows folded, such as "128 workloads in
    /// 40 namespaces", when its facts together run longer than a box
    /// shows at a glance, though each alone is short.
    pub folded: Option<String>,
}

/// Where a section is drawn on the board: what the object belongs to and
/// what names it to its left, its own facts in its box, and what it
/// names to its right.  A section with a table spans the page under
/// the board, whatever its side.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Side {
    BelongsTo,
    Into,
    Own,
    Out,
}

/// One label and what it names: objects with their pages, or words.
#[derive(Clone, Debug, PartialEq)]
pub struct Fact {
    pub label: String,
    pub values: Vec<Reference>,
    pub shape: Shape,
}

/// How a fact's values are drawn.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Shape {
    /// Names, each a link when it has a page.
    Names,
    /// Literal text, such as a label selector, set in code.
    Literal,
    /// No value, such as a printer column whose field is not written
    /// yet: a gray dash.
    Unset,
    /// Names in pairs, each a thing and the machine it is on, such as a
    /// pod that mounts a volume: "web-1 on alpha".
    Placed,
    /// Addresses outside transect, each a link that opens in a new tab,
    /// such as a storage console.
    External,
    /// A template that other values are filled from, set in code and in
    /// gray, because it shows how the values were found and is not one
    /// of them.
    Template,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Table {
    pub head: Vec<String>,
    pub rows: Vec<Vec<String>>,
}

impl Section {
    pub fn new(title: &str, side: Side) -> Self {
        Self {
            title: title.into(),
            side,
            facts: Vec::new(),
            table: None,
            none: None,
            folded: None,
        }
    }

    /// The section folded behind `summary`, which opens to its facts.
    pub fn folded(self, summary: String) -> Self {
        Self {
            folded: Some(summary),
            ..self
        }
    }

    /// The section with `label` naming `values`, left out when `values`
    /// is empty.
    pub fn fact(self, label: &str, values: Vec<Reference>) -> Self {
        self.shaped(label, values, Shape::Names)
    }

    /// The section with `label` giving `values` as literal text.
    pub fn literals(self, label: &str, values: Vec<String>) -> Self {
        self.shaped(
            label,
            values.into_iter().map(words).collect(),
            Shape::Literal,
        )
    }

    /// The section with `label` linking each of `urls`, which open in
    /// a new tab, left out when there are none.
    pub fn external(self, label: &str, urls: Vec<String>) -> Self {
        let values = urls.into_iter().map(|url| Reference {
            text: url.clone(),
            href: Some(url),
        });
        self.shaped(label, values.collect(), Shape::External)
    }

    /// The section with `label` giving `template` in gray, when there
    /// is one.
    pub fn template(self, label: &str, template: Option<String>) -> Self {
        self.shaped(
            label,
            template.into_iter().map(words).collect(),
            Shape::Template,
        )
    }

    /// The section with `label` showing `text`, when there is any.
    pub fn text(self, label: &str, text: Option<String>) -> Self {
        let values = text.into_iter().map(|text| Reference { text, href: None });
        self.fact(label, values.collect())
    }

    /// The section with `label` naming each of `pairs` as a thing and
    /// the place it is on, left out when `pairs` is empty.
    pub fn placed(self, label: &str, pairs: Vec<(Reference, Reference)>) -> Self {
        let values = pairs.into_iter().flat_map(|(what, on)| [what, on]);
        self.shaped(label, values.collect(), Shape::Placed)
    }

    /// The section with `label` and no value, drawn as a gray dash.
    pub fn unset(mut self, label: &str) -> Self {
        self.facts.push(Fact {
            label: label.into(),
            values: Vec::new(),
            shape: Shape::Unset,
        });
        self
    }

    fn shaped(mut self, label: &str, values: Vec<Reference>, shape: Shape) -> Self {
        if !values.is_empty() {
            self.facts.push(Fact {
                label: label.into(),
                values,
                shape,
            });
        }
        self
    }

    pub fn with_table(self, table: Table) -> Self {
        Self {
            table: Some(table),
            ..self
        }
    }

    /// The section with the line it shows when it has nothing else.
    pub fn or(self, none: &str) -> Self {
        self.or_off(OffLine::said(none))
    }

    /// The section with `none` as the line it shows when it has nothing
    /// else, which may send the reader to `/resources`.
    pub fn or_off(self, none: OffLine) -> Self {
        Self {
            none: Some(none),
            ..self
        }
    }

    /// Whether the section has anything to show, its `none` line aside.
    pub fn is_empty(&self) -> bool {
        self.facts.is_empty()
            && self
                .table
                .as_ref()
                .is_none_or(|table| table.rows.is_empty())
    }

    /// The section without the values that `shown` reports another part
    /// of the page already draws, and without each fact that this leaves
    /// empty.
    pub fn without(self, shown: impl Fn(&Reference) -> bool) -> Self {
        let facts = self.facts.into_iter().filter_map(|fact| {
            let unset = fact.shape == Shape::Unset;
            let values: Vec<Reference> = fact.values.into_iter().filter(|v| !shown(v)).collect();
            (unset || !values.is_empty()).then_some(Fact { values, ..fact })
        });
        Self {
            facts: facts.collect(),
            ..self
        }
    }

    /// Whether it has the object's own facts, which show as a grid of
    /// labels beside values.
    pub fn is_own(&self) -> bool {
        self.side == Side::Own
    }

    /// Whether the page draws it: it has something to show, or a line
    /// that tells the reader it has nothing.
    pub fn shown(&self) -> bool {
        !self.is_empty() || self.none.is_some()
    }
}

impl Fact {
    /// Whether its values fold to a count, as a list longer than a
    /// box can show at a glance does.
    pub fn folds(&self) -> bool {
        self.count() > MOST
    }

    /// How many things it names, a pair of a thing and its place
    /// counting once.
    pub fn count(&self) -> usize {
        match self.shape {
            Shape::Placed => self.values.len() / 2,
            _ => self.values.len(),
        }
    }

    /// Its values in pairs, for a fact that places each thing.
    pub fn pairs(&self) -> Vec<(&Reference, &Reference)> {
        let (pairs, _) = self.values.as_chunks::<2>();
        pairs.iter().map(|[a, b]| (a, b)).collect()
    }
}

/// Words that open no page.
pub fn words(text: impl Into<String>) -> Reference {
    Reference {
        text: text.into(),
        href: None,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn named(count: usize) -> Vec<Reference> {
        (0..count).map(|n| words(format!("name-{n}"))).collect()
    }

    #[rstest]
    #[case::a_value(vec![words("a")], 1)]
    #[case::nothing(vec![], 0)]
    fn leaves_out_a_fact_with_nothing_to_name(
        #[case] values: Vec<Reference>,
        #[case] facts: usize,
    ) {
        let section = Section::new("About", Side::BelongsTo).fact("names", values);
        assert_eq!(section.facts.len(), facts);
    }

    #[rstest]
    #[case::empty_with_no_line(Section::new("Rules", Side::Own), false)]
    #[case::empty_with_a_line(Section::new("Rules", Side::Own).or("No rules."), true)]
    #[case::a_fact(Section::new("Rules", Side::Own).text("verbs", Some("get".into())), true)]
    #[case::an_unset_value(Section::new("Status", Side::Own).unset("Phase"), true)]
    fn draws_a_section_with_something_to_say(#[case] section: Section, #[case] shown: bool) {
        assert_eq!(section.shown(), shown);
    }

    #[rstest]
    fn leaves_out_what_another_part_of_the_page_draws() {
        let section = Section::new("Refers to", Side::Out)
            .fact("depends on", vec![words("drawn"), words("not drawn")])
            .fact("holds", vec![words("drawn")]);
        let section = section.without(|value| value.text == "drawn");
        let facts: Vec<(&str, usize)> = section
            .facts
            .iter()
            .map(|fact| (fact.label.as_str(), fact.values.len()))
            .collect();
        assert_eq!(facts, [("depends on", 1)]);
    }

    #[rstest]
    #[case::a_few(6, false)]
    #[case::more_than_a_box_shows(7, true)]
    fn folds_a_long_list_of_placed_things_by_the_things(#[case] count: usize, #[case] folds: bool) {
        let pairs = named(count).into_iter().map(|what| (what, words("alpha")));
        let section = Section::new("Mounted by", Side::Into).placed("writes", pairs.collect());
        assert_eq!(section.facts[0].folds(), folds);
    }

    #[rstest]
    #[case::a_few(6, false)]
    #[case::more_than_a_box_shows(7, true)]
    fn folds_a_long_list_to_a_count(#[case] count: usize, #[case] folds: bool) {
        let section = Section::new("Refers to", Side::Out).fact("allowed to call", named(count));
        assert_eq!(section.facts[0].folds(), folds);
    }
}
