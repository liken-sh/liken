//! The sentences of the cascade: what the dependents of a down workload
//! lose.
//!
//! A dependent is an object with an edge that points at the down
//! workload: a workload that calls it, an application that signs its
//! users in through it, a host that an `Ingress` routes to it, or a
//! custom resource that it controls. A `Hop` is one dependent and the
//! down workloads that it loses, and each `Hop` becomes one line.
//!
//! Intent is what the people who run the cluster write about its parts,
//! in annotations, overlays, and `Intent` resources, which
//! `transect_resources::intent` reads. When a line shows a fact from
//! intent that the API does not report, the line ends with the mark
//! `intent`, and hovering the mark shows where the fact was written.
//! The legend under the lines explains each mark.

use std::collections::BTreeMap;
use std::fmt;

use super::{SIGN_IN, list, object};
use crate::loss::cascade::{Dependent, Hop, IfDown};
use crate::loss::sidebar::{Hops, Sidebar, Unreconciled};
use transect_graph::edges::{Object, Provenance};
use transect_graph::links::{Linker, Part, Piece, listed, text};

impl Sidebar {
    /// "What else fails with it", or "with them" for several machines.
    pub fn cascade_heading(&self) -> String {
        let (pronoun, _) = self.machines.pronoun();
        format!("What else fails with {}", object(pronoun))
    }

    /// The legend that explains the marks on the cascade's lines, or
    /// `None` when no line has a mark.
    pub fn legend(&self) -> Option<String> {
        transect_graph::edges::provenance::legend(self.cascade.iter().flat_map(Hops::marks))
    }
}

impl fmt::Display for Hops {
    /// What follows the namespace's name in a cascade line: "web can't
    /// reach db; shop.example.com stops answering".
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&texts(&self.hops, self.namespace.as_deref()).join("; "))
    }
}

impl Hops {
    /// Where each fact from intent in the line's hops was written, each
    /// place once, for the marks after the line.
    pub fn marks(&self) -> Vec<&Provenance> {
        let mut marks: Vec<&Provenance> = Vec::new();
        for provenance in self.hops.iter().flat_map(Hop::provenances) {
            if !marks.contains(&provenance) {
                marks.push(provenance);
            }
        }
        marks
    }
}

impl Hop {
    /// The line for one dependent, as pieces.  An object outside `here`
    /// is named with its namespace.  When `kind` is true, the dependent
    /// is named with its kind, so that two dependents with the same name
    /// read as two.  Each object is its own piece, so a page links each
    /// object's name and never a word of a workload's `provides` text
    /// from intent.
    pub fn pieces(&self, here: Option<&str>, kind: bool) -> Vec<Piece> {
        match self.subject(here, kind) {
            Some(mut pieces) => {
                pieces.push(Piece::words(" "));
                pieces.extend(self.predicate(here, false));
                pieces
            }
            None => self.whole(here),
        }
    }

    /// The name of the caller or the application at the start of the
    /// line.  Returns `None` for a host or a custom resource, because
    /// `whole` writes the whole line for those.  The name includes the
    /// kind when `kind` is true, or when a lost workload has the same
    /// name as the dependent.
    fn subject(&self, here: Option<&str>, kind: bool) -> Option<Vec<Piece>> {
        let (Dependent::Caller(object) | Dependent::SignsIn(object)) = &self.dependent else {
            return None;
        };
        Some(
            match kind || self.lost.iter().any(|lost| self.shares_name(lost)) {
                true => kinded(object, here),
                false => named(object, here),
            },
        )
    }

    /// Whether `lost` is a different object with the dependent's name.
    fn shares_name(&self, lost: &Object) -> bool {
        let dependent = self.dependent.object();
        dependent.is_some_and(|object| object.name == lost.name && object != lost)
    }

    /// What a caller or application loses, written after its name:
    /// "loses order storage and can't reach db", or "lose" when
    /// `plural`.  A lost workload with a `provides` text from intent
    /// appears as that text after "loses".  Any other lost workload
    /// appears as a name after "can't reach", with its kind when it has
    /// the dependent's name.
    fn predicate(&self, here: Option<&str>, plural: bool) -> Vec<Piece> {
        let loses = if plural { "lose" } else { "loses" };
        if let Dependent::SignsIn(_) = self.dependent {
            return vec![Piece::Words(format!("{loses} {SIGN_IN}"))];
        }
        let mut provided: Vec<String> = Vec::new();
        let mut reached: Vec<Vec<Piece>> = Vec::new();
        for lost in &self.lost {
            match (self.provides.get(lost), self.shares_name(lost)) {
                (Some(provides), _) => provided.push(provides.text.clone()),
                (None, true) => reached.push(kinded(lost, here)),
                (None, false) => reached.push(named(lost, here)),
            }
        }
        provided.dedup();
        let mut losing = Vec::new();
        if !provided.is_empty() {
            let provided = list(provided.iter().map(String::as_str));
            losing.push(Piece::Words(format!("{loses} {provided}")));
        }
        if !reached.is_empty() {
            if !losing.is_empty() {
                losing.push(Piece::words(" and "));
            }
            losing.push(Piece::words("can't reach "));
            losing.extend(listed(reached));
        }
        losing
    }

    /// The line of a host or a custom resource.  It is never combined
    /// with the line of another dependent.
    fn whole(&self, here: Option<&str>) -> Vec<Piece> {
        let mut pieces = Vec::new();
        match &self.dependent {
            Dependent::Host(host) => pieces.push(Piece::Words(format!("{host} stops answering"))),
            Dependent::Controlled(resource) => {
                let verb = if self.lost.len() == 1 {
                    "returns"
                } else {
                    "return"
                };
                pieces.push(Piece::words("Nothing reconciles "));
                pieces.extend(kinded(resource, here));
                pieces.push(Piece::words(" until "));
                pieces.extend(listed(self.lost.iter().map(|l| named(l, here)).collect()));
                pieces.push(Piece::Words(format!(" {verb}")));
            }
            Dependent::Caller(_) | Dependent::SignsIn(_) => {}
        }
        pieces
    }
}

/// One line of what stops if a workload goes down, with where each fact
/// from intent in it was written, for its marks.
#[derive(Debug)]
pub struct IfDownLine<'a> {
    pub pieces: Vec<Piece>,
    pub marks: Vec<&'a Provenance>,
}

impl Sidebar {
    /// "Until deploy/applier returns, nothing reconciles Bucket
    /// shop/charts and 3 Trees."  A kind with more than one object
    /// appears as a count, linked to the kind's page when its plural is
    /// known, and a kind with one object appears as that object.
    pub fn unreconciled_pieces(&self, line: &Unreconciled) -> Vec<Piece> {
        let verb = if line.controllers.len() == 1 {
            "returns"
        } else {
            "return"
        };
        let mut pieces = vec![Piece::words("Until ")];
        pieces.extend(listed(
            line.controllers.iter().map(|c| named(c, None)).collect(),
        ));
        pieces.push(Piece::Words(format!(" {verb}, nothing reconciles ")));
        let mut by_kind: Vec<(&Object, usize)> = Vec::new();
        for object in &line.objects {
            match by_kind.last_mut() {
                Some((first, count))
                    if (&first.group, &first.kind) == (&object.group, &object.kind) =>
                {
                    *count += 1
                }
                _ => by_kind.push((object, 1)),
            }
        }
        let kinds = by_kind.into_iter().map(|(first, count)| {
            if count == 1 {
                return kinded(first, None);
            }
            let key = (first.group.clone(), first.kind.clone());
            let plural = self.plurals.get(&key);
            let label = transect_resources::knowledge::plural_label(
                &first.kind,
                plural.map(String::as_str),
            );
            let words = format!("{count} {label}");
            vec![match plural {
                Some(plural) => {
                    Piece::Page(words, transect_graph::links::kind(&first.group, plural))
                }
                None => Piece::Words(words),
            }]
        });
        pieces.extend(listed(kinds.collect()));
        pieces.push(Piece::words("."));
        pieces
    }

    pub fn cascade_parts(&self, line: &Hops) -> Vec<Part> {
        line.parts(&self.linker)
    }
}

impl Hops {
    pub fn parts(&self, linker: &Linker) -> Vec<Part> {
        let lines = pieces(&self.hops, self.namespace.as_deref());
        let joined: Vec<Piece> = lines
            .into_iter()
            .enumerate()
            .flat_map(|(at, line)| {
                let joint = (at > 0).then(|| Piece::words("; "));
                joint.into_iter().chain(line)
            })
            .collect();
        linker.link(&joined)
    }
}

impl IfDown {
    pub fn heading(&self) -> &'static str {
        "If it goes down"
    }

    /// One line per dependent, with names relative to the workload's own
    /// namespace, each with where its facts from intent were written.
    /// Dependents that lose the same thing share one line: "celery and
    /// beat lose the web app".  When the workload controls more than one
    /// custom resource of a kind, the kind gets one line at the position
    /// of its first resource: "Nothing reconciles 3 Clusters until
    /// operator returns".
    pub fn lines(&self) -> Vec<IfDownLine<'_>> {
        let here = self.namespace.as_deref();
        let mut by_kind: BTreeMap<(&str, &str), Vec<&Hop>> = BTreeMap::new();
        for hop in &self.hops {
            if let Dependent::Controlled(resource) = &hop.dependent {
                let kind = (resource.group.as_str(), resource.kind.as_str());
                by_kind.entry(kind).or_default().push(hop);
            }
        }
        let kind_of = |hop| counted_kind(hop, &by_kind);
        let single: Vec<&Hop> = self
            .hops
            .iter()
            .filter(|hop| kind_of(hop).is_none())
            .collect();
        let mut lines = Vec::new();
        let mut shared: Vec<Shared> = Vec::new();
        let mut counted = std::collections::BTreeSet::new();
        for hop in &self.hops {
            match kind_of(hop) {
                None => {
                    let named_alike = shares_name(hop, &single);
                    let Some(subject) = hop.subject(here, named_alike) else {
                        lines.push(IfDownLine {
                            pieces: hop.pieces(here, named_alike),
                            marks: hop.provenances(),
                        });
                        continue;
                    };
                    let predicate = hop.predicate(here, false);
                    match shared.iter_mut().find(|line| line.predicate == predicate) {
                        Some(line) => line.add(subject, hop),
                        None => {
                            shared.push(Shared::new(lines.len(), subject, predicate, hop));
                            lines.push(IfDownLine {
                                pieces: Vec::new(),
                                marks: Vec::new(),
                            });
                        }
                    }
                }
                Some(kind) if counted.insert(kind) => {
                    lines.push(self.counted(kind, &by_kind[&kind], here));
                }
                Some(_) => {}
            }
        }
        for line in shared {
            let at = line.at;
            lines[at] = line.line(here);
        }
        lines
    }

    /// The line for the custom resources of `kind` in `hops`: their
    /// count, linked to the kind's page when its plural is known, and
    /// the lost workloads that reconcile them.
    fn counted(
        &self,
        (group, kind): (&str, &str),
        hops: &[&Hop],
        here: Option<&str>,
    ) -> IfDownLine<'_> {
        let mut lost: Vec<&Object> = hops.iter().flat_map(|hop| &hop.lost).collect();
        lost.sort();
        lost.dedup();
        let verb = if lost.len() == 1 { "returns" } else { "return" };
        let plural = self.plurals.get(&(group.to_string(), kind.to_string()));
        let label = transect_resources::knowledge::plural_label(kind, plural.map(String::as_str));
        let count = format!("{} {label}", hops.len());
        let mut pieces = vec![Piece::words("Nothing reconciles ")];
        pieces.push(match plural {
            Some(plural) => Piece::Page(count, transect_graph::links::kind(group, plural)),
            None => Piece::Words(count),
        });
        pieces.push(Piece::words(" until "));
        pieces.extend(listed(lost.iter().map(|lost| named(lost, here)).collect()));
        pieces.push(Piece::Words(format!(" {verb}")));
        IfDownLine {
            pieces,
            marks: Vec::new(),
        }
    }

    pub fn line_parts(&self, line: &IfDownLine) -> Vec<Part> {
        self.linker.link(&line.pieces)
    }

    /// The text that the card shows in place of the lines when no edge
    /// reaches the workload, or `None` when there are lines.
    pub fn nothing_text(&self) -> Option<&'static str> {
        self.hops
            .is_empty()
            .then_some("Nothing in the cluster depends on it.")
    }
}

/// The dependents that lose the same thing.  One line names them
/// together, at the position of the first of them.  `at` is the index
/// of that line in the lines that `IfDown::lines` returns.
struct Shared<'a> {
    at: usize,
    subjects: Vec<Vec<Piece>>,
    predicate: Vec<Piece>,
    hops: Vec<&'a Hop>,
}

impl<'a> Shared<'a> {
    fn new(at: usize, subject: Vec<Piece>, predicate: Vec<Piece>, hop: &'a Hop) -> Self {
        Self {
            at,
            subjects: vec![subject],
            predicate,
            hops: vec![hop],
        }
    }

    fn add(&mut self, subject: Vec<Piece>, hop: &'a Hop) {
        self.subjects.push(subject);
        self.hops.push(hop);
    }

    /// "web loses the app", or "web, feed, and cron lose the app", with
    /// where the facts from intent in every hop were written, each place
    /// once.
    fn line(self, here: Option<&str>) -> IfDownLine<'a> {
        let plural = self.subjects.len() > 1;
        let mut pieces = listed(self.subjects);
        pieces.push(Piece::words(" "));
        pieces.extend(self.hops[0].predicate(here, plural));
        let mut marks: Vec<&Provenance> = Vec::new();
        for mark in self.hops.iter().flat_map(|hop| hop.provenances()) {
            if !marks.contains(&mark) {
                marks.push(mark);
            }
        }
        IfDownLine { pieces, marks }
    }
}

/// Whether another of `hops` has a dependent with `hop`'s name that is a
/// different object.  An application that both calls a workload and
/// signs in through it is one object in two hops, so it does not count
/// as sharing its own name.
fn shares_name(hop: &Hop, hops: &[&Hop]) -> bool {
    let name = hop.dependent.name();
    let object = hop.dependent.object();
    hops.iter()
        .any(|other| other.dependent.name() == name && other.dependent.object() != object)
}

/// The kind of the custom resource that `hop` reaches, when `by_kind`
/// has more than one resource of that kind.  `IfDown::lines` writes one
/// line with the count of those resources in place of a line for each.
fn counted_kind<'h>(
    hop: &'h Hop,
    by_kind: &BTreeMap<(&str, &str), Vec<&Hop>>,
) -> Option<(&'h str, &'h str)> {
    let Dependent::Controlled(resource) = &hop.dependent else {
        return None;
    };
    let kind = (resource.group.as_str(), resource.kind.as_str());
    (by_kind[&kind].len() > 1).then_some(kind)
}

fn texts(hops: &[Hop], here: Option<&str>) -> Vec<String> {
    let pieces = pieces(hops, here);
    pieces.iter().map(|pieces| text(pieces)).collect()
}

/// A dependent is named with its kind when another dependent in `hops`
/// has the same name.
fn pieces(hops: &[Hop], here: Option<&str>) -> Vec<Vec<Piece>> {
    let all: Vec<&Hop> = hops.iter().collect();
    hops.iter()
        .map(|hop| hop.pieces(here, shares_name(hop, &all)))
        .collect()
}

/// `object`'s kind and name, with its namespace when it is not `here`:
/// "Player den", "Catalog media/catalog".  The kind is plain words and
/// the name is the link.
fn kinded(object: &Object, here: Option<&str>) -> Vec<Piece> {
    let full = transect_graph::links::kind_and_name_in(object, here);
    let name = transect_graph::links::name_in(object, here);
    match full.strip_suffix(&name) {
        Some(kind) if !kind.is_empty() => {
            vec![Piece::Words(kind.into()), Piece::Name(object.clone(), name)]
        }
        _ => vec![Piece::Name(object.clone(), full)],
    }
}

/// `object`'s name, with its namespace when it is not `here`: "db",
/// "shop/db".
fn named(object: &Object, here: Option<&str>) -> Vec<Piece> {
    vec![Piece::Name(
        object.clone(),
        transect_graph::links::name_in(object, here),
    )]
}

#[cfg(test)]
mod tests;
