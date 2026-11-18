//! One account's row of the accounts matrix: a mark for each
//! capability, the roles that grant them, and the step by which the
//! account gets a capability that no role grants it directly.

use super::super::account::{Account, Hop, Where};
use super::super::capability::ALL;
use super::super::{Access, Capability, Cell, Held};
use super::words;
use transect_graph::edges::Object;
use transect_resources::intent::Line;

/// How many namespaces a short block lists by name.  Its title lists
/// all of them.
const NAMED: usize = 2;

#[derive(Debug, PartialEq)]
pub struct Row {
    pub name: String,
    pub href: Option<String>,
    /// Why the account is bound, from its intent.
    pub why: Option<Line>,
    pub cells: Vec<Option<Mark>>,
    /// The first role that grants the account any capability.
    pub role: Option<Named>,
    /// How many other roles grant it one, and their names.
    pub more: Option<(usize, String)>,
    /// The step behind the first outlined mark that the heading above
    /// the row does not already explain.
    pub step: Option<Step>,
    /// Whether the next row has the same marks, so the page draws no
    /// rule between them.
    pub tight: bool,
}

/// An object's name and the page it links to, when it has one.
#[derive(Clone, Debug, PartialEq)]
pub struct Named {
    pub text: String,
    pub href: Option<String>,
}

/// A step in words.  `before` and `after` surround the account that the
/// step goes through, which the page can link, and `title` explains the
/// step in full.
#[derive(Debug, PartialEq)]
pub struct Step {
    pub before: &'static str,
    pub account: Option<Named>,
    pub after: &'static str,
    pub title: String,
}

/// A cell's mark.  A filled block means that a binding grants the
/// capability, and an outlined block means that it follows from another
/// grant.  A full-width block covers every namespace.  A short block
/// covers some, and `scope` lists them.
#[derive(Debug, PartialEq)]
pub struct Mark {
    pub granted: bool,
    pub everywhere: bool,
    pub scope: Option<String>,
    pub title: String,
}

impl Row {
    /// The "through" cell as one line of text: the role, how many other
    /// roles, and the step.  The page shows it as the hover text of a
    /// cell that the column width cuts off.
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

/// What a row needs besides the access: the page of each object and the
/// intent of each account.
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

    /// `account` by its short name, for a line that already says that
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

/// The row of `account`, whose access is `held`.  When the heading of
/// the rows above names a step, `through` is that step, and the first
/// five capabilities follow from it in every namespace.
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

/// The step behind the row's first outlined mark: its step in every
/// namespace when it has one, and otherwise its step in the first
/// namespace.  Under a heading that names a step, the heading already
/// explains the first five capabilities, so the row skips them, and it
/// returns no step that reads the same as the heading's.
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

/// The mark of `capability` for `account`.  The mark shows the most
/// that the account may do, so a grant in every namespace wins over a
/// step in every namespace, which wins over a grant in some namespaces.
/// A full-width mark needs a grant or step that no list of object names
/// limits.
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

/// The text of a short block: the first `NAMED` namespaces, how many
/// more there are, and "by name" when the grants there are limited to
/// named objects.
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
