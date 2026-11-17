//! `/access/accounts` has a row for every account that a binding grants
//! one of the six capabilities.  The rows are in three runs: the
//! accounts bound to a role with every verb on every resource, the
//! accounts from which that follows, by the account they go through,
//! and every other account.  Kubernetes' own controllers are folded
//! into one line, and two gray lines count the accounts that have none
//! of the six.

mod cells;
mod here;
mod words;

use std::collections::{BTreeMap, BTreeSet};

use askama::Template;

use super::account::{Account, Hop, Where};
use super::capability::ALL;
use super::grants::EVERYONE;
use super::{Access, Capability, Held};
pub use cells::{Look, Named, Row};
pub use here::rows as namespace_rows;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::links::Part;

/// One column: the capability's name, and its verbs on hover.
#[derive(Debug, PartialEq)]
pub struct Column {
    pub name: &'static str,
    pub verbs: String,
}

/// Rows that share the value that their heading names.
#[derive(Debug, PartialEq)]
pub struct Run {
    pub heading: Vec<Part>,
    pub rows: Vec<Row>,
}

/// A gray line that counts accounts, with their names on hover.
#[derive(Debug, PartialEq)]
pub struct Counted {
    pub text: String,
    pub names: String,
}

#[derive(Template)]
#[template(path = "access/accounts/accounts.html", blocks = ["main"])]
pub struct AccountsPage {
    pub columns: Vec<Column>,
    pub runs: Vec<Run>,
    /// The accounts whose every binding names a role Kubernetes ships.
    pub controllers: Vec<Row>,
    /// The line that states whether the groups that every account is in
    /// have any of the six.
    pub everyone: String,
    pub none: Option<Counted>,
    pub unbound: Option<Counted>,
    /// Whether no kind knowledge fills the sixth column.
    pub no_gitops: bool,
}

impl AccountsPage {
    pub fn build(access: &Access, look: &Look) -> Self {
        let runs = Runs::sort(access);
        let rows = |accounts: &[&Account], heading: Option<&Hop>| {
            let row = |a: &&Account| cells::row(access, a, &access.accounts[*a], heading, look);
            accounts.iter().map(row).collect::<Vec<_>>()
        };
        let mut built: Vec<Run> = Vec::new();
        if !runs.everything.is_empty() {
            let mut heading = vec![Part::plain("Bound to ")];
            heading.extend(listed(
                runs.roles
                    .iter()
                    .map(|role| look_role(role, look))
                    .collect(),
            ));
            built.push(Run {
                heading,
                rows: rows(&runs.everything, None),
            });
        }
        // A run through an admin's account contains the accounts from
        // which the first five capabilities follow in every namespace,
        // by the step that the run's heading names.  So a row names a
        // step only where its own step differs.
        for (hop, accounts) in &runs.through {
            built.push(Run {
                heading: heading(hop, access, look),
                rows: rows(accounts, Some(hop)),
            });
        }
        if !runs.other.is_empty() {
            built.push(Run {
                heading: vec![Part::plain("Every other account granted one of the six")],
                rows: rows(&runs.other, None),
            });
        }
        let columns = ALL
            .iter()
            .map(|capability| Column {
                name: capability.words(),
                verbs: capability.verbs(&access.gitops),
            })
            .collect();
        Self {
            columns,
            runs: built,
            controllers: rows(&runs.controllers, None),
            everyone: everyone(access),
            none: counted(
                &runs.none,
                "account is",
                "accounts are",
                "bound to roles that grant none of the six",
            ),
            unbound: counted(
                &runs.unbound,
                "ServiceAccount is",
                "ServiceAccounts are",
                "named by no binding",
            ),
            no_gitops: access.gitops.is_empty(),
        }
    }

    /// How many rows the three runs contain.
    pub fn count(&self) -> usize {
        self.runs.iter().map(|run| run.rows.len()).sum()
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        vec![
            Crumb::home(),
            Crumb::link("access", "/access".into()),
            Crumb::current("accounts"),
        ]
    }

    pub fn events_url(&self) -> String {
        "/events/access/accounts".into()
    }
}

/// The accounts of each run, and the role the first run is bound to.
struct Runs<'a> {
    everything: Vec<&'a Account>,
    roles: BTreeSet<transect_graph::edges::Object>,
    /// The accounts from which every verb on every resource follows, by
    /// the first hop toward an account bound to it.
    through: BTreeMap<Hop, Vec<&'a Account>>,
    other: Vec<&'a Account>,
    controllers: Vec<&'a Account>,
    none: Vec<&'a Account>,
    unbound: Vec<&'a Account>,
}

impl<'a> Runs<'a> {
    fn sort(access: &'a Access) -> Self {
        let mut runs = Self {
            everything: Vec::new(),
            roles: BTreeSet::new(),
            through: BTreeMap::new(),
            other: Vec::new(),
            controllers: Vec::new(),
            none: Vec::new(),
            unbound: Vec::new(),
        };
        let bound_everywhere = |held: &Held| {
            let roles = held.everything.iter();
            roles
                .filter(|(at, _)| *at == Where::Everywhere)
                .map(|(_, role)| role.clone())
                .collect::<Vec<_>>()
        };
        let top: BTreeSet<&Account> = access
            .accounts
            .iter()
            .filter(|(_, held)| !bound_everywhere(held).is_empty())
            .map(|(account, _)| account)
            .collect();
        for (account, held) in &access.accounts {
            let shipped = !held.bound.is_empty() && held.bound.iter().all(|bound| bound.defaults);
            if !held.holds_any() {
                match held.bound.is_empty() {
                    true if matches!(account, Account::ServiceAccount { .. }) => {
                        runs.unbound.push(account)
                    }
                    true => {}
                    false => runs.none.push(account),
                }
                continue;
            }
            if shipped {
                runs.controllers.push(account);
            } else if top.contains(account) {
                runs.everything.push(account);
                runs.roles.extend(bound_everywhere(held));
            } else if let Some(hop) = toward(held, &top) {
                runs.through.entry(hop).or_default().push(account);
            } else {
                runs.other.push(account);
            }
        }
        runs
    }
}

/// The hop by which `held` may do the first five capabilities of `ALL`
/// in every namespace, or `None` when it may not do all five there.
/// When a binding grants it to run pods or change workloads in the
/// namespace of an account of `top`, the hop is running as that
/// account.  Otherwise it is the hop of a follow for one of the five
/// that no binding grants everywhere: the first that leads to an
/// account of `top`, or else the last.
fn toward(held: &Held, top: &BTreeSet<&Account>) -> Option<Hop> {
    let five = &ALL[..5];
    let everywhere =
        |capability: &Capability| held.unnamed(*capability).contains(&Where::Everywhere);
    if !five.iter().all(everywhere) {
        return None;
    }
    // Only a basis that a binding grants is a first hop, because a
    // basis that follows has a first hop of its own, which the code
    // below finds.
    for basis in [Capability::RunPods, Capability::ChangeWorkloads] {
        let granted = held.cells.get(&basis).into_iter();
        for at in granted.flat_map(|cell| cell.granted_unnamed()) {
            let beside = top.iter().find(|account| match account {
                Account::ServiceAccount { namespace, .. } => at.covers(namespace),
                _ => false,
            });
            if let Some(Account::ServiceAccount { namespace, .. }) = beside {
                return Some(Hop::RunsAs {
                    account: (*beside?).clone(),
                    basis: Capability::RunPods,
                    at: Where::In(namespace.clone()),
                });
            }
        }
    }
    let hops = five.iter().filter_map(|capability| {
        let cell = held.cells.get(capability)?;
        let granted = cell.granted_unnamed().any(|at| *at == Where::Everywhere);
        let follow = cell.follows.get(&Where::Everywhere).filter(|_| !granted)?;
        Some(&follow.hop)
    });
    let hops: Vec<&Hop> = hops.collect();
    let into_top = hops
        .iter()
        .find(|hop| hop.account().is_some_and(|a| top.contains(a)));
    into_top.or(hops.last()).map(|hop| match hop {
        Hop::RunsAs { account, at, .. } => Hop::RunsAs {
            account: account.clone(),
            basis: Capability::RunPods,
            at: at.clone(),
        },
        other => (*other).clone(),
    })
}

/// A run's heading for the accounts that go through `hop`.
fn heading(hop: &Hop, access: &Access, look: &Look) -> Vec<Part> {
    let part = |named: Named| Part {
        text: named.text,
        href: named.href,
    };
    let namespace = |at: &Where| match at {
        Where::In(namespace) => Some(Part {
            text: format!("Namespace {namespace}"),
            href: (look.href)(&transect_graph::edges::Object::new(
                "",
                "Namespace",
                None,
                namespace,
            )),
        }),
        Where::Everywhere => None,
    };
    let mut parts: Vec<Part> = Vec::new();
    match hop {
        Hop::RunsAs { account, at, .. } => {
            parts.push(Part::plain("May run pods or change workloads in "));
            parts.extend(namespace(at));
            parts.push(Part::plain(", and so may run as "));
            parts.push(part(look.named(account)));
        }
        Hop::ExecToken { account, at } => {
            parts.push(Part::plain("May exec into pods in "));
            parts.extend(namespace(at));
            parts.push(Part::plain(" that run as "));
            parts.push(part(look.named(account)));
            parts.push(Part::plain(" and mount its token"));
        }
        Hop::Applies { account, .. } => {
            parts.push(Part::plain("May change what "));
            parts.push(part(look.named(account)));
            parts.push(Part::plain(" applies"));
        }
        Hop::MintsToken { account, .. } => {
            parts.push(Part::plain("May create a token for "));
            parts.push(part(look.named(account)));
        }
        Hop::Impersonates { account } => {
            parts.push(Part::plain("May impersonate "));
            parts.push(part(look.named(account)));
        }
        Hop::Binds { role, at } => {
            parts.push(Part::plain("May bind "));
            parts.push(look_role(role, look));
            parts.push(Part {
                text: format!(" to itself {}", at.words()),
                href: None,
            });
            return parts;
        }
        Hop::Rewrites { role, at } => {
            parts.push(Part::plain("May rewrite "));
            parts.push(look_role(role, look));
            parts.push(Part {
                text: format!(", bound to it {}", at.words()),
                href: None,
            });
            return parts;
        }
        Hop::StartsPod { .. } | Hop::ExecMounts { .. } => return parts,
    }
    let Some(through) = hop.account().and_then(|account| access.of(account)) else {
        return parts;
    };
    let roles: Vec<Part> = through
        .everything
        .iter()
        .filter(|(at, _)| *at == Where::Everywhere)
        .map(|(_, role)| look_role(role, look))
        .collect();
    if roles.is_empty() {
        return parts;
    }
    parts.push(Part::plain(", which is bound to "));
    parts.extend(listed(roles));
    parts
}

fn look_role(role: &transect_graph::edges::Object, look: &Look) -> Part {
    Part {
        text: words::named(role),
        href: crate::access::link(role, look.href),
    }
}

/// `parts` joined as an English list: "a and b", "a, b, and c".
fn listed(parts: Vec<Part>) -> Vec<Part> {
    let count = parts.len();
    let mut joined = Vec::new();
    for (at, part) in parts.into_iter().enumerate() {
        match (count, at) {
            (_, 0) => {}
            (2, _) => joined.push(Part::plain(" and ")),
            (_, at) if at + 1 == count => joined.push(Part::plain(", and ")),
            _ => joined.push(Part::plain(", ")),
        }
        joined.push(part);
    }
    joined
}

/// The line that states whether the groups that every account is in
/// have any of the six.
fn everyone(access: &Access) -> String {
    let holding: Vec<String> = EVERYONE
        .iter()
        .filter(|group| {
            let held = access.of(&Account::Group(group.to_string()));
            held.is_some_and(Held::holds_any)
        })
        .map(|group| format!("Group {group}"))
        .collect();
    match holding.as_slice() {
        [] => "No binding grants any of the six to Group system:authenticated or Group system:serviceaccounts, the groups every account is in.".into(),
        groups => format!("{} may do some of these six, and every account is in it, so every account may do them too.", groups.join(" and ")),
    }
}

/// "3 accounts are bound to roles that grant none of the six", with
/// their names on hover, or none when there are none.
fn counted(accounts: &[&Account], one: &str, many: &str, rest: &str) -> Option<Counted> {
    let verb = match accounts.len() {
        0 => return None,
        1 => one,
        _ => many,
    };
    let names: Vec<String> = accounts.iter().map(|account| account.text()).collect();
    Some(Counted {
        text: format!("{} {verb} {rest}.", accounts.len()),
        names: names.join(", "),
    })
}
