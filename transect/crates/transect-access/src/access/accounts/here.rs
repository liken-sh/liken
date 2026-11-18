//! The rows of one namespace's accounts matrix: one row for each
//! account that has any capability in `ALL` in the namespace.  A row
//! also shows the role that grants a capability, or the step that the
//! capability follows by.  Rows with the same marks are next to each
//! other.

use super::super::account::{Account, Where};
use super::super::{ALL, Access, Capability, Cell, Held};
use super::cells::{Look, Mark, Row, Step};
use super::words;
use transect_graph::edges::Object;

/// The rows of the accounts that may act in `namespace`, and,
/// separately, the rows of Kubernetes' own components and controllers,
/// which are the accounts bound only by the default bindings.
pub fn rows(access: &Access, namespace: &str, look: &Look) -> (Vec<Row>, Vec<Row>) {
    let mut rows: Vec<(Vec<u8>, Row)> = Vec::new();
    let mut controllers: Vec<(Vec<u8>, Row)> = Vec::new();
    for (account, held) in &access.accounts {
        let marks: Vec<Option<Mark>> = ALL
            .iter()
            .map(|capability| mark(access, account, *capability, held, namespace))
            .collect();
        if marks.iter().all(Option::is_none) {
            continue;
        }
        let key: Vec<u8> = marks
            .iter()
            .map(|mark| match mark {
                Some(Mark { granted: true, .. }) => 0,
                Some(_) => 1,
                None => 2,
            })
            .collect();
        let row = row(access, account, held, marks, namespace, look);
        let shipped = !held.bound.is_empty() && held.bound.iter().all(|bound| bound.defaults);
        match shipped {
            true => controllers.push((key, row)),
            false => rows.push((key, row)),
        }
    }
    (tighten(rows), tighten(controllers))
}

/// Sorts `rows` by their marks and then by name, and sets `tight` on
/// each row whose next row has the same marks.
fn tighten(mut rows: Vec<(Vec<u8>, Row)>) -> Vec<Row> {
    rows.sort_by(|(a, row_a), (b, row_b)| (a, &row_a.name).cmp(&(b, &row_b.name)));
    let keys: Vec<Vec<u8>> = rows.iter().map(|(key, _)| key.clone()).collect();
    rows.into_iter()
        .enumerate()
        .map(|(at, (key, mut row))| {
            row.tight = keys.get(at + 1) == Some(&key);
            row
        })
        .collect()
}

/// The mark for `capability` in `namespace`, or `None` when the account
/// does not have it there.  It is filled when a binding grants the
/// capability in the namespace, and outlined when it only follows from
/// other capabilities.  It is labeled "by name" when every grant or
/// step is limited to named objects.
fn mark(
    access: &Access,
    account: &Account,
    capability: Capability,
    held: &Held,
    namespace: &str,
) -> Option<Mark> {
    let cell: &Cell = held.cells.get(&capability)?;
    let granted = cell.granted.iter().filter(|(at, _)| at.covers(namespace));
    let vias: Vec<(&Where, &super::super::Via)> = granted
        .flat_map(|(at, vias)| vias.iter().map(move |via| (at, via)))
        .collect();
    let unnamed = vias.iter().find(|(_, via)| via.names.is_empty());
    let named = |by_name: bool| by_name.then(|| "by name".to_string());
    if let Some((at, via)) = unnamed.or(vias.first()) {
        return Some(Mark {
            granted: true,
            everywhere: true,
            scope: named(!via.names.is_empty()),
            title: words::granted(capability, at, via),
        });
    }
    let follows = cell.follows.iter().filter(|(at, _)| at.covers(namespace));
    let follows: Vec<(&Where, &crate::access::Follow)> = follows.collect();
    let unnamed = follows.iter().find(|(_, follow)| follow.names.is_empty());
    let (at, follow) = unnamed.or(follows.first())?;
    Some(Mark {
        granted: false,
        everywhere: true,
        scope: named(!follow.names.is_empty()),
        title: words::follows(access, account, capability, at, follow),
    })
}

/// The row for `account` in `namespace`: its marks, the first role that
/// grants it a capability there, a count and list of any other such
/// roles, and the step of the first capability that only follows.
fn row(
    access: &Access,
    account: &Account,
    held: &Held,
    marks: Vec<Option<Mark>>,
    namespace: &str,
    look: &Look,
) -> Row {
    let mut roles: Vec<&Object> = Vec::new();
    for capability in ALL {
        let cells = held.cells.get(&capability).into_iter();
        let granted = cells.flat_map(|cell| cell.granted.iter());
        let vias = granted.filter(|(at, _)| at.covers(namespace));
        for via in vias.flat_map(|(_, vias)| vias) {
            if !roles.contains(&&via.role) {
                roles.push(&via.role);
            }
        }
    }
    let more = (roles.len() > 1).then(|| {
        let others: Vec<String> = roles[1..].iter().map(|role| words::named(role)).collect();
        (others.len(), others.join(", "))
    });
    let step = ALL
        .iter()
        .zip(&marks)
        .find(|(_, mark)| mark.as_ref().is_some_and(|mark| !mark.granted))
        .and_then(|(capability, _)| {
            let cell = held.cells.get(capability)?;
            let (at, follow) = cell.follows.iter().find(|(at, _)| at.covers(namespace))?;
            let (before, through, after) = words::step(&follow.hop);
            Some(Step {
                before,
                account: through.map(|through| look.short(through)),
                after,
                title: words::follows(access, account, *capability, at, follow),
            })
        });
    let named = look.named(account);
    Row {
        name: account.text(),
        href: named.href,
        why: account.object().and_then(|object| (look.why)(&object)),
        cells: marks,
        role: roles.first().map(|role| look.object(role)),
        more,
        step,
        tight: false,
    }
}
