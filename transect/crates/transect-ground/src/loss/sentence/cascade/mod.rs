//! The text of the cascade: what the dependents of a down workload lose.

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

    /// What the marks on the cascade's lines mean, when any is marked.
    pub fn legend(&self) -> Option<String> {
        transect_graph::edges::provenance::legend(self.cascade.iter().flat_map(Hops::marks))
    }
}

impl fmt::Display for Hops {
    /// What follows the namespace's name: "web can't reach db; shop.example.com
    /// stops answering".
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&texts(&self.hops, self.namespace.as_deref()).join("; "))
    }
}

impl Hops {
    /// Where the line's stored text was written, each once, for the
    /// provenance marks after the line.
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
    /// The line for one dependent, naming objects outside `here` with
    /// their namespace.  A caller whose name another in its line shares
    /// is named with its kind, `kind`, so the two read as two.  Each
    /// object it names is kept apart from the words, so a page links the
    /// objects and never a word inside what intent declares a workload
    /// provides.
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

    /// The caller or the application that a line starts with, or `None`
    /// for the line of a host or a custom resource, which starts with no
    /// object.  The subject is named with its kind when `kind` is true
    /// because another dependent has the same name, or when an object
    /// that it loses has the same name.
    fn subject(&self, here: Option<&str>, kind: bool) -> Option<Vec<Piece>> {
        let (Dependent::Caller(object) | Dependent::SignsIn(object)) = &self.dependent else {
            return None;
        };
        Some(
            match kind || self.lost.iter().any(|lost| self.alike(lost)) {
                true => kinded(object, here),
                false => named(object, here),
            },
        )
    }

    /// Whether `lost` is another object of the dependent's name.
    fn alike(&self, lost: &Object) -> bool {
        let dependent = self.dependent.object();
        dependent.is_some_and(|object| object.name == lost.name && object != lost)
    }

    /// What a caller or an application loses, after its name: "loses
    /// order storage and can't reach db", or "lose" for several.  A lost
    /// workload with a `provides` text from intent appears as that text
    /// after "loses".  Any other lost workload appears as a name after
    /// "can't reach", with its kind when the caller has the same name.
    fn predicate(&self, here: Option<&str>, plural: bool) -> Vec<Piece> {
        let loses = if plural { "lose" } else { "loses" };
        if let Dependent::SignsIn(_) = self.dependent {
            return vec![Piece::Words(format!("{loses} {SIGN_IN}"))];
        }
        let mut provided: Vec<String> = Vec::new();
        let mut reached: Vec<Vec<Piece>> = Vec::new();
        for lost in &self.lost {
            match (self.provides.get(lost), self.alike(lost)) {
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

    /// The line of a host or a custom resource, which is never joined
    /// with another line.
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

/// One line of what stops if a workload goes down, with where its
/// stored text was written.
#[derive(Debug)]
pub struct IfDownLine<'a> {
    pub pieces: Vec<Piece>,
    pub marks: Vec<&'a Provenance>,
}

impl Sidebar {
    /// "Until applier in deploy returns, nothing reconciles 12
    /// Bundles and Source shop.".  A kind with more than one object
    /// appears as a count linked to the kind's page, and a kind with one
    /// object appears as that object's name.
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

    /// A cascade line with the objects its hops name linked.
    pub fn cascade_parts(&self, line: &Hops) -> Vec<Part> {
        line.parts(&self.linker)
    }
}

impl Hops {
    /// The line with a link on each object that its hops name, to the
    /// page that `linker` returns.  The names come from the hops and not
    /// from a parse of the line's text, so a word in a workload's
    /// `provides` text never becomes a link to a workload of that name.
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
    /// namespace, each with where its stored text was written.  The
    /// dependents that lose the same thing share one line: "celery and
    /// beat lose the web app".  When the workload controls more than one
    /// custom resource of a kind, the kind gets one line, in the place
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
                    let named_alike = alike(hop, &single);
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

    /// The one line for the objects of `kind` that `hops` reach, its
    /// count linked to the kind's page.
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

    /// `line` with the objects it names linked, and the count of a
    /// kind's objects linked to the kind's page.
    pub fn line_parts(&self, line: &IfDownLine) -> Vec<Part> {
        self.linker.link(&line.pieces)
    }

    /// The text that the card shows in place of the lines when no edge
    /// reaches the workload.
    pub fn nothing_text(&self) -> Option<&'static str> {
        self.hops
            .is_empty()
            .then_some("Nothing in the cluster depends on it.")
    }
}

/// The dependents that lose the same thing.  One line names them
/// together, at the position of the first of them.
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
    /// the marks of every hop in the line, each once.
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

/// Whether another of `hops` has a dependent of `hop`'s name that is not
/// the same object.  An application that both calls a workload and
/// signs in through it is one dependent in two lines, so its name alone
/// is enough.
fn alike(hop: &Hop, hops: &[&Hop]) -> bool {
    let name = hop.dependent.name();
    let object = hop.dependent.object();
    hops.iter()
        .any(|other| other.dependent.name() == name && other.dependent.object() != object)
}

/// The kind of the custom resource that `hop` reaches, when `by_kind`
/// has more than one resource of that kind, so that one line counts
/// them.
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

/// Each hop's line, which names a dependent with its kind when another
/// dependent in `hops` has the same name.
fn texts(hops: &[Hop], here: Option<&str>) -> Vec<String> {
    let pieces = pieces(hops, here);
    pieces.iter().map(|pieces| text(pieces)).collect()
}

/// Each hop's line as `texts` writes it, in pieces.  A dependent is
/// named with its kind when another in `hops` has the same name.
fn pieces(hops: &[Hop], here: Option<&str>) -> Vec<Vec<Piece>> {
    let all: Vec<&Hop> = hops.iter().collect();
    hops.iter()
        .map(|hop| hop.pieces(here, alike(hop, &all)))
        .collect()
}

/// `object`'s kind and name, with its namespace when it is not `here`:
/// "Player den", "Catalog media/catalog".  The kind is words and the
/// name is the link.
fn kinded(object: &Object, here: Option<&str>) -> Vec<Piece> {
    let said = transect_graph::links::kind_and_name_in(object, here);
    let name = transect_graph::links::name_in(object, here);
    match said.strip_suffix(&name) {
        Some(kind) if !kind.is_empty() => {
            vec![Piece::Words(kind.into()), Piece::Name(object.clone(), name)]
        }
        _ => vec![Piece::Name(object.clone(), said)],
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
