//! The sections of an object's board. A section is a titled list of
//! facts, each a label with its values, and it can also have a table
//! for facts that have columns, such as a Role's rules.

use transect_graph::links::Reference;
use transect_resources::resources::OffLine;

/// The most values a fact lists. A fact with more shows a count, which
/// the reader expands to see the values.
const FOLD_AFTER: usize = 6;

#[derive(Clone, Debug, PartialEq)]
pub struct Section {
    pub title: String,
    pub side: Side,
    pub facts: Vec<Fact>,
    pub table: Option<Table>,
    /// The line shown when the section has no facts or table rows.
    pub none: Option<OffLine>,
    /// A summary, such as "128 workloads in 40 namespaces", that the
    /// section shows in place of its facts until the reader expands it.
    /// It is for a section of many facts, each short enough not to
    /// collapse on its own.
    pub folded: Option<String>,
}

/// Where a section is drawn on the board. On the left, `BelongsTo` lists
/// what the object belongs to, and `Into`, under it, lists what refers
/// to the object. `Own` is in the object's box. `Out`, on the right,
/// lists what the object refers to. A section with a table spans the
/// page under the board, whatever its side.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Side {
    BelongsTo,
    Into,
    Own,
    Out,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Fact {
    pub label: String,
    pub values: Vec<Reference>,
    pub shape: Shape,
}

/// How a fact's values are drawn.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Shape {
    /// Names, each a link when its object has a page.
    Names,
    /// Literal text, such as a label selector.
    Literal,
    /// No value, such as a printer column whose field is not set, drawn
    /// as a gray dash.
    Unset,
    /// Names in pairs, each a thing and the machine it is on, such as a
    /// pod that mounts a volume: "web-1 on alpha".
    Placed,
    /// Addresses outside transect, such as a storage console, each a
    /// link that opens in a new tab.
    External,
    /// A template that other values were generated from, drawn in gray
    /// because it is not one of those values.
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

    pub fn folded(self, summary: String) -> Self {
        Self {
            folded: Some(summary),
            ..self
        }
    }

    /// Adds a fact named `label` that lists `values`. Adds nothing when
    /// `values` is empty.
    pub fn fact(self, label: &str, values: Vec<Reference>) -> Self {
        self.shaped(label, values, Shape::Names)
    }

    /// Adds a fact named `label` that lists `values` as literal text.
    pub fn literals(self, label: &str, values: Vec<String>) -> Self {
        self.shaped(
            label,
            values.into_iter().map(words).collect(),
            Shape::Literal,
        )
    }

    /// Adds a fact named `label` that links each of `urls`, which open in
    /// a new tab. Adds nothing when `urls` is empty.
    pub fn external(self, label: &str, urls: Vec<String>) -> Self {
        let values = urls.into_iter().map(|url| Reference {
            text: url.clone(),
            href: Some(url),
        });
        self.shaped(label, values.collect(), Shape::External)
    }

    /// Adds a fact named `label` that shows `template` in gray.
    /// Adds nothing when `template` is none.
    pub fn template(self, label: &str, template: Option<String>) -> Self {
        self.shaped(
            label,
            template.into_iter().map(words).collect(),
            Shape::Template,
        )
    }

    /// Adds a fact named `label` that shows `text`. Adds nothing when
    /// `text` is none.
    pub fn text(self, label: &str, text: Option<String>) -> Self {
        let values = text.into_iter().map(|text| Reference { text, href: None });
        self.fact(label, values.collect())
    }

    /// Adds a fact named `label` that lists `pairs`, each a thing and the
    /// place it is on. Adds nothing when `pairs` is empty.
    pub fn placed(self, label: &str, pairs: Vec<(Reference, Reference)>) -> Self {
        let values = pairs.into_iter().flat_map(|(what, on)| [what, on]);
        self.shaped(label, values.collect(), Shape::Placed)
    }

    /// Adds a fact named `label` with no value, drawn as a gray dash.
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

    /// Sets `none` as the line shown when the section is empty.
    pub fn or(self, none: &str) -> Self {
        self.or_off(OffLine::plain(none))
    }

    /// The same as `or`, with an `OffLine`, which can link to
    /// `/resources`.
    pub fn or_off(self, none: OffLine) -> Self {
        Self {
            none: Some(none),
            ..self
        }
    }

    /// Whether the section has no facts and no table rows. Its `none`
    /// line does not count.
    pub fn is_empty(&self) -> bool {
        self.facts.is_empty()
            && self
                .table
                .as_ref()
                .is_none_or(|table| table.rows.is_empty())
    }

    /// Removes the values for which `shown` is true, and each fact that
    /// this leaves with no values. A fact with
    /// `Shape::Unset` has no values to begin with, so it stays.
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

    /// Whether this is the object's own section, which is drawn as a
    /// grid of labels beside values.
    pub fn is_own(&self) -> bool {
        self.side == Side::Own
    }

    /// Whether the page draws the section: it is not empty, or it has a
    /// `none` line to say that it is.
    pub fn shown(&self) -> bool {
        !self.is_empty() || self.none.is_some()
    }
}

impl Fact {
    pub fn folds(&self) -> bool {
        self.count() > FOLD_AFTER
    }

    /// How many things the fact names. A `Placed` pair counts once.
    pub fn count(&self) -> usize {
        match self.shape {
            Shape::Placed => self.values.len() / 2,
            _ => self.values.len(),
        }
    }

    /// The values as (thing, place) pairs, for a `Placed` fact.
    pub fn pairs(&self) -> Vec<(&Reference, &Reference)> {
        let (pairs, _) = self.values.as_chunks::<2>();
        pairs.iter().map(|[a, b]| (a, b)).collect()
    }
}

/// A `Reference` to plain text with no link.
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
