//! One row of an accounts matrix: a mark for each capability, the roles
//! that grant them, and the step that the capabilities that follow go
//! through.

use super::super::account::{Account, Hop, Where};
use super::super::capability::ALL;
use super::super::{Access, Capability, Cell, Held};
use super::words;
use transect_graph::edges::Object;
use transect_resources::intent::Line;

/// The most namespaces that a short block names beside it.  The rest
/// are in its title.
const NAMED: usize = 2;

/// One account's row.
#[derive(Debug, PartialEq)]
pub struct Row {
    pub name: String,
    pub href: Option<String>,
    /// Why the account is bound as it is, from its intent.
    pub why: Option<Line>,
    pub cells: Vec<Option<Mark>>,
    /// The first role that grants it one of the six, the others' count,
    /// and their names.
    pub role: Option<Named>,
    pub more: Option<(usize, String)>,
    /// The step that its first capability that follows goes through.
    pub step: Option<Step>,
    /// Whether the row below has the same marks, so no rule is drawn
    /// between them.
    pub tight: bool,
}

/// An object's name and the page it links to, when it has one.
#[derive(Clone, Debug, PartialEq)]
pub struct Named {
    pub text: String,
    pub href: Option<String>,
}

/// The step that a row's capabilities follow by, as words before and
/// after the account that the step goes through.
#[derive(Debug, PartialEq)]
pub struct Step {
    pub before: &'static str,
    pub account: Option<Named>,
    pub after: &'static str,
    pub title: String,
}

/// A cell's mark: a filled block where a binding grants the capability,
/// an outlined one where it follows, full width for every namespace and
/// short for some, named beside it.
#[derive(Debug, PartialEq)]
pub struct Mark {
    pub granted: bool,
    pub everywhere: bool,
    pub scope: Option<String>,
    pub title: String,
}

impl Row {
    /// The "through" cell as one line of text: the role, how many
    /// others, and the step, for the hover on a cell that the column's
    /// width cuts short.
    pub fn line(&self) -> Option<String> {
        let mut line = self
            .role
            .as_ref()
            .map(|role| role.text.clone())
            .unwrap_or_default();
        if let Some((count, _)) = &self.more {
            line.push_str(&format!(" +{count}"));
        }
        if let Some(step) = &self.step {
            let account = step
                .account
                .as_ref()
                .map(|a| a.text.as_str())
                .unwrap_or_default();
            if !line.is_empty() {
                line.push_str(" → ");
            }
            line.push_str(&format!("{}{account}{}", step.before, step.after));
        }
        (!line.is_empty()).then_some(line)
    }
}

impl Mark {
    pub fn class(&self) -> &'static str {
        match (self.granted, self.everywhere) {
            (true, true) => "ax-block ax-block-d",
            (true, false) => "ax-block ax-block-d ax-block-some",
            (false, true) => "ax-block ax-block-f",
            (false, false) => "ax-block ax-block-f ax-block-some",
        }
    }
}

/// What a row reads besides the access: each object's page, and the
/// intent on each account.
pub struct Look<'a> {
    pub href: &'a dyn Fn(&Object) -> Option<String>,
    pub why: &'a dyn Fn(&Object) -> Option<Line>,
}

impl Look<'_> {
    pub fn named(&self, account: &Account) -> Named {
        Named {
            text: account.text(),
            href: match account.object() {
                Some(object) => crate::access::link(&object, self.href),
                None => account.subject_page(),
            },
        }
    }

    /// `account` by its name alone, for a line that already states that
    /// it is an account.
    pub fn short(&self, account: &Account) -> Named {
        Named {
            text: account.short(),
            ..self.named(account)
        }
    }

    pub fn object(&self, object: &Object) -> Named {
        Named {
            text: words::named(object),
            href: crate::access::link(object, self.href),
        }
    }
}

/// The row of `account`, whose access is `held`, in a run whose heading
/// names `through`, the step from which the first five follow in every
/// namespace, when it names one.
pub fn row(
    access: &Access,
    account: &Account,
    held: &Held,
    through: Option<&Hop>,
    look: &Look,
) -> Row {
    let cells: Vec<Option<Mark>> = ALL
        .iter()
        .map(|capability| mark(access, account, *capability, held.cells.get(capability)))
        .collect();
    let mut roles: Vec<&Object> = Vec::new();
    for capability in ALL {
        let vias = held
            .cells
            .get(&capability)
            .into_iter()
            .flat_map(|cell| cell.granted.values());
        for via in vias.flatten() {
            if !roles.contains(&&via.role) {
                roles.push(&via.role);
            }
        }
    }
    let more = (roles.len() > 1).then(|| {
        let others: Vec<String> = roles[1..].iter().map(|role| words::named(role)).collect();
        (others.len(), others.join(", "))
    });
    let named = account.object();
    Row {
        name: account.text(),
        href: look.named(account).href,
        why: named.as_ref().and_then(|object| (look.why)(object)),
        role: roles.first().map(|role| look.object(role)),
        more,
        step: step(access, account, held, &cells, through, look),
        cells,
        tight: false,
    }
}

/// The step of the first capability whose mark is outlined, with the
/// step in every namespace when the capability follows there.  Under a
/// heading that names `heading`, the first five capabilities follow by
/// that step, and a step with the same words is the heading's step, so
/// the row names neither of them.
fn step(
    access: &Access,
    account: &Account,
    held: &Held,
    marks: &[Option<Mark>],
    heading: Option<&Hop>,
    look: &Look,
) -> Option<Step> {
    let explained = if heading.is_some() { &ALL[..5] } else { &[] };
    let (capability, _) = ALL.iter().zip(marks).find(|(capability, mark)| {
        !explained.contains(capability) && mark.as_ref().is_some_and(|mark| !mark.granted)
    })?;
    let cell = held.cells.get(capability)?;
    let everywhere = cell.follows.get_key_value(&Where::Everywhere);
    let (at, follow) = everywhere.or_else(|| cell.follows.iter().next())?;
    let words = words::step(&follow.hop);
    if heading.is_some_and(|hop| words::step(hop) == words) {
        return None;
    }
    let (before, through, after) = words;
    Some(Step {
        before,
        account: through.map(|through| look.short(through)),
        after,
        title: words::follows(access, account, *capability, at, follow),
    })
}

/// The mark of `capability` for `account`.  A grant in every namespace
/// is drawn before a step in every namespace, which is drawn before a
/// grant in some, so a mark always shows the most that the account may
/// do.
fn mark(
    access: &Access,
    account: &Account,
    capability: Capability,
    cell: Option<&Cell>,
) -> Option<Mark> {
    let cell = cell.filter(|cell| !cell.is_empty())?;
    let everywhere = &Where::Everywhere;
    let unnamed = cell
        .granted
        .get(everywhere)
        .and_then(|vias| vias.iter().find(|v| v.names.is_empty()));
    if let Some(via) = unnamed {
        return Some(Mark {
            granted: true,
            everywhere: true,
            scope: None,
            title: words::granted(capability, everywhere, via),
        });
    }
    if let Some(follow) = cell.follows.get(everywhere).filter(|f| f.names.is_empty()) {
        return Some(Mark {
            granted: false,
            everywhere: true,
            scope: None,
            title: words::follows(access, account, capability, everywhere, follow),
        });
    }
    if !cell.granted.is_empty() {
        let titles = cell.granted.iter().flat_map(|(at, vias)| {
            vias.iter()
                .map(move |via| words::granted(capability, at, via))
        });
        let named = cell
            .granted
            .values()
            .flatten()
            .any(|via| !via.names.is_empty());
        let unnamed = cell
            .granted
            .values()
            .flatten()
            .any(|via| via.names.is_empty());
        let by_name = named && !unnamed;
        return Some(Mark {
            granted: true,
            everywhere: false,
            scope: Some(scope(cell.granted.keys(), by_name)),
            title: titles.collect::<Vec<_>>().join(" "),
        });
    }
    let titles = cell
        .follows
        .iter()
        .map(|(at, follow)| words::follows(access, account, capability, at, follow));
    let by_name = cell.follows.values().all(|follow| !follow.names.is_empty());
    Some(Mark {
        granted: false,
        everywhere: false,
        scope: Some(scope(cell.follows.keys(), by_name)),
        title: titles.collect::<Vec<_>>().join(" "),
    })
}

/// The namespaces a short block names: the first two, and how many
/// more, with "by name" when every grant there names its objects.
fn scope<'a>(places: impl Iterator<Item = &'a Where>, by_name: bool) -> String {
    let names: Vec<&str> = places
        .map(|place| match place {
            Where::Everywhere => "every namespace",
            Where::In(namespace) => namespace.as_str(),
        })
        .collect();
    let mut text = names
        .iter()
        .take(NAMED)
        .copied()
        .collect::<Vec<_>>()
        .join(", ");
    if names.len() > NAMED {
        text.push_str(&format!(" +{}", names.len() - NAMED));
    }
    if by_name {
        text.push_str(", by name");
    }
    text
}
