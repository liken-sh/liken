//! An account's page: a `ServiceAccount`'s, at its own URL, and a
//! group's or a user's, under the accounts matrix.  The drawing reads
//! what runs as the account, its bindings and the rules of their
//! roles, and what follows, from the access the model reduced.

use std::collections::HashMap;

use k8s_openapi::jiff::Timestamp;

use super::followed;
use super::look::{Look, and};
use crate::web::model::Model;
use transect_access::access::account::{AUTHENTICATED, SERVICE_ACCOUNTS};
use transect_access::access::drawing::account::{self as drawn, Grant, Placing, Scene};
use transect_access::access::drawing::account_page::{AccountPage, Place};
use transect_access::access::rules;
use transect_access::access::{Account, Capability, Held, Scope, Via, Where};
use transect_graph::edges::{Object, Shown};
use transect_graph::links::Part;
use transect_map::map::section_drawing::{Look as BoxLook, SectionBox};
use transect_resources::layers::Layers;

/// A filled block, beside one of the six a binding grants.
pub const GRANTED: &str = "ax-block ax-block-d ax-block-some";
/// An outlined block, beside one of the six that follows.
pub const FOLLOWS: &str = "ax-block ax-block-f ax-block-some";

/// The page of `ServiceAccount` `namespace/name`, or none when the
/// cluster holds no such `ServiceAccount`.
pub fn service_account_page(
    model: &Model,
    layers: &Layers,
    namespace: &str,
    name: &str,
    now: Timestamp,
) -> Option<AccountPage> {
    let snapshot = &model.snapshot;
    let meta = snapshot.paged.service_accounts.iter().find(|found| {
        found.metadata.namespace.as_deref() == Some(namespace)
            && found.metadata.name.as_deref() == Some(name)
    })?;
    let object = Object::new("", "ServiceAccount", Some(namespace), name);
    let account = Account::service_account(namespace, name);
    let look = Look::new(model);
    let graph = model.graph(&Shown::default());
    let card = super::super::object::card::card(
        snapshot,
        &object,
        Some(&meta.metadata),
        &graph,
        None,
        false,
        now,
    );
    let runs = runs(model, namespace, name, &look);
    Some(AccountPage {
        place: Place::Namespace {
            namespace: namespace.into(),
            layer: snapshot.layer_of(namespace, layers)?,
        },
        title: transect_graph::edges::kind_and_name("ServiceAccount", name),
        name: name.into(),
        path: transect_graph::links::page(&object, &snapshot.plurals)?,
        run_as: Some(run_as(model, &account, &runs)),
        drawing: drawn::draw(scene(model, &account, runs.boxes, &look)),
        card: Some(card),
        no_gitops: model.access.gitops.is_empty(),
    })
}

/// The page of a group or a user, by the kind in its URL, `groups` or
/// `users`, or none when no binding names it.
pub fn subject_page(model: &Model, kinds: &str, name: &str) -> Option<AccountPage> {
    let account = match kinds {
        "groups" => Account::Group(name.into()),
        "users" => Account::User(name.into()),
        _ => return None,
    };
    model.access.of(&account)?;
    let look = Look::new(model);
    let (head, members) = members(model, &account, &look);
    let mut scene = scene(model, &account, members, &look);
    scene.runs_head = head;
    Some(AccountPage {
        place: Place::Accounts,
        title: account.text(),
        name: name.into(),
        path: look.account(&account)?,
        run_as: None,
        drawing: drawn::draw(scene),
        card: None,
        no_gitops: model.access.gitops.is_empty(),
    })
}

/// The workloads whose pods run as a `ServiceAccount`, as boxes and as a
/// sentence names them, with how many pods each runs.
struct Runs {
    boxes: Vec<Placing>,
    named: Vec<(Part, usize)>,
}

fn runs(model: &Model, namespace: &str, name: &str, look: &Look) -> Runs {
    let snapshot = &model.snapshot;
    let mut owners: Vec<(Object, usize, Vec<String>)> = Vec::new();
    let pods = snapshot.pods.iter().filter(|pod| {
        let spec = pod.spec.as_ref();
        pod.metadata.namespace.as_deref() == Some(namespace)
            && !transect_graph::pods::finished(pod)
            && spec
                .and_then(|s| s.service_account_name.as_deref())
                .unwrap_or("default")
                == name
    });
    for pod in pods {
        let Some(pod_name) = pod.metadata.name.as_deref() else {
            continue;
        };
        let object = Object::new("", "Pod", Some(namespace), pod_name);
        let owner = snapshot.ownership.top_owner(&object);
        let node = pod.spec.as_ref().and_then(|s| s.node_name.clone());
        let at = match owners.iter().position(|(o, _, _)| *o == owner) {
            Some(at) => at,
            None => {
                owners.push((owner, 0, Vec::new()));
                owners.len() - 1
            }
        };
        owners[at].1 += 1;
        owners[at].2.extend(node);
    }
    owners.sort_by(|a, b| a.0.cmp(&b.0));
    let mut boxes = Vec::new();
    let mut named = Vec::new();
    for (owner, count, mut nodes) in owners {
        nodes.sort();
        nodes.dedup();
        let line = match (count, nodes.as_slice()) {
            (1, [node]) => format!("1 pod on {node}"),
            (count, [node]) => format!("{count} pods on {node}"),
            (1, _) => "1 pod".to_string(),
            (count, nodes) if nodes.len() > 1 => {
                format!("{count} pods on {} machines", nodes.len())
            }
            (count, _) => format!("{count} pods"),
        };
        let title = transect_graph::edges::kind_and_name(&owner.kind, &owner.name);
        let href = look.href(&owner);
        boxes.push(Placing::plain(
            SectionBox::new(title.clone(), vec![line], BoxLook::Plain).with_href(href.clone()),
        ));
        named.push((Part { text: title, href }, count));
    }
    if boxes.is_empty() {
        boxes.push(Placing::none(SectionBox::new(
            "no pod runs as it".into(),
            Vec::new(),
            BoxLook::Plain,
        )));
    }
    Runs { boxes, named }
}

/// Who is in a group, which the API server states only for the groups
/// it puts every account in, and who signs in as a user, which it never
/// states.
fn members(model: &Model, account: &Account, look: &Look) -> (&'static str, Vec<Placing>) {
    let accounts = model.access.accounts.keys();
    let service_accounts = accounts.filter(|a| matches!(a, Account::ServiceAccount { .. }));
    let count = |namespace: Option<&str>| {
        let here = service_accounts.clone().filter(|a| match (a, namespace) {
            (Account::ServiceAccount { namespace: at, .. }, Some(namespace)) => at == namespace,
            _ => true,
        });
        match here.count() {
            1 => "1 ServiceAccount".to_string(),
            count => format!("{count} ServiceAccounts"),
        }
    };
    let boxed = |title: String, line: String, href: Option<String>| {
        Placing::plain(SectionBox::new(title, vec![line], BoxLook::Plain).with_href(href))
    };
    let none =
        |title: &str| Placing::none(SectionBox::new(title.into(), Vec::new(), BoxLook::Plain));
    match account {
        Account::Group(name) => {
            let prefix = format!("{SERVICE_ACCOUNTS}:");
            let member = match name.as_str() {
                AUTHENTICATED => boxed(
                    "every account that signs in".into(),
                    "every ServiceAccount, and every user".into(),
                    Some("/access/accounts".into()),
                ),
                SERVICE_ACCOUNTS => boxed(
                    "every ServiceAccount".into(),
                    count(None),
                    Some("/access/accounts".into()),
                ),
                name => match name.strip_prefix(&prefix) {
                    Some(namespace) => boxed(
                        format!("every ServiceAccount in {namespace}"),
                        count(Some(namespace)),
                        look.namespace(namespace),
                    ),
                    None => none("the API does not list who is in it"),
                },
            };
            ("in it", vec![member])
        }
        _ => (
            "signs in as it",
            vec![none("the API does not list who signs in as it")],
        ),
    }
}

/// "Deployment web runs as it, in 2 pods.  A pod names any
/// ServiceAccount in its own namespace, so the 3 other accounts …"
fn run_as(model: &Model, account: &Account, runs: &Runs) -> Vec<Part> {
    let Account::ServiceAccount { namespace, .. } = account else {
        return Vec::new();
    };
    let mut parts = Vec::new();
    let pods: usize = runs.named.iter().map(|(_, count)| count).sum();
    for (at, (named, _)) in runs.named.iter().enumerate() {
        if at > 0 {
            parts.push(Part::plain(match at + 1 == runs.named.len() {
                true => " and ",
                false => ", ",
            }));
        }
        parts.push(named.clone());
    }
    if !runs.named.is_empty() {
        let verb = match runs.named.len() {
            1 => "runs",
            _ => "run",
        };
        let pods = match pods {
            1 => "1 pod".to_string(),
            pods => format!("{pods} pods"),
        };
        parts.push(Part::plain(&format!(" {verb} as it, in {pods}. ")));
    }
    let others = model
        .access
        .accounts
        .iter()
        .filter(|(other, held)| {
            *other != account
                && [Capability::RunPods, Capability::ChangeWorkloads]
                    .iter()
                    .any(|capability| {
                        let places = held.unnamed(*capability);
                        places.iter().any(|at| at.covers(namespace))
                    })
        })
        .count();
    let others = match others {
        0 => format!(
            "No other account may run pods or change workloads in {namespace}, so none may run as it, though a pod names any ServiceAccount in its own namespace. "
        ),
        1 => format!(
            "A pod names any ServiceAccount in its own namespace, so the 1 other account that may run pods or change workloads in {namespace} may run as it. "
        ),
        count => format!(
            "A pod names any ServiceAccount in its own namespace, so the {count} other accounts that may run pods or change workloads in {namespace} may run as it. "
        ),
    };
    parts.push(Part::plain(&others));
    parts.push(Part {
        text: format!("The access of Namespace {namespace}"),
        href: Some(format!(
            "{}/access",
            transect_graph::links::namespace(namespace)
        )),
    });
    parts.push(Part::plain(" lists them."));
    parts
}

/// One binding of the account: its binding, its role, where it holds,
/// and the group it names when it names one.
struct Binding {
    binding: Object,
    role: Object,
    at: Where,
    group: Option<String>,
}

/// Every binding that names the account, or a group it is in that
/// grants it one of the six.
fn bindings(held: &Held) -> Vec<Binding> {
    let at = |binding: &Object| match &binding.namespace {
        Some(namespace) => Where::In(namespace.clone()),
        None => Where::Everywhere,
    };
    let mut found: Vec<Binding> = held
        .bound
        .iter()
        .map(|bound| Binding {
            binding: bound.binding.clone(),
            role: bound.role.clone(),
            at: at(&bound.binding),
            group: None,
        })
        .collect();
    let vias = held
        .cells
        .values()
        .flat_map(|cell| cell.granted.values())
        .flatten();
    for Via {
        binding,
        role,
        group,
        ..
    } in vias.filter(|via| via.group.is_some())
    {
        if !found.iter().any(|b| b.binding == *binding) {
            found.push(Binding {
                binding: binding.clone(),
                role: role.clone(),
                at: at(binding),
                group: group.clone(),
            });
        }
    }
    found.sort_by(|a, b| (&a.at, &a.role).cmp(&(&b.at, &b.role)));
    found
}

/// The drawing of `account`, with `runs` in its first column.
fn scene(model: &Model, account: &Account, runs: Vec<Placing>, look: &Look) -> Scene {
    let access = &model.access;
    let empty = Held::default();
    let held = access.of(account).unwrap_or(&empty);
    let here = match account {
        Account::ServiceAccount { namespace, .. } => Some(namespace.as_str()),
        _ => None,
    };
    let mut lines = Vec::new();
    let mut mark = None;
    if let Some(why) = account
        .object()
        .and_then(|object| model.snapshot.intents.of(&object)?.why.clone())
    {
        lines.push(why.text);
        mark = Some(why.provenance);
    }
    // On its own page an account is named without its namespace, which
    // the page's crumb and switcher name.
    let title = match account {
        Account::ServiceAccount { name, .. } => {
            transect_graph::edges::kind_and_name("ServiceAccount", name)
        }
        other => other.text(),
    };
    let mut boxed = SectionBox::new(title, lines, BoxLook::Workload);
    if let Some(mark) = mark {
        boxed = boxed.marked(mark);
    }
    let kinds = kind_names(model);
    let label = |group: &str, plural: &str| match kinds.get(&(group, plural)) {
        Some(kind) => transect_resources::knowledge::plural_label(kind, Some(plural)),
        None => plural.to_string(),
    };
    let mut roles: Vec<Placing> = Vec::new();
    let mut grants: Vec<Grant> = Vec::new();
    for (at, binding) in bindings(held).into_iter().enumerate() {
        // A `Role` holds only in its own namespace, which its title
        // names; a `ClusterRole` holds where its binding puts it.
        let mut lines = match binding.role.namespace {
            Some(_) => Vec::new(),
            None => vec![binding.at.words()],
        };
        lines.extend(
            binding
                .group
                .map(|group| format!("as a member of Group {group}")),
        );
        let hint = format!(
            "{} binds it to {}, so the role applies {}.",
            transect_graph::links::kind_and_name_in(&binding.binding, None),
            transect_graph::links::kind_and_name_in(&binding.role, None),
            binding.at.words()
        );
        let title = transect_graph::links::kind_and_name_in(&binding.role, here);
        roles.push(Placing::plain(
            SectionBox::new(title, lines, BoxLook::Plain)
                .with_href(look.href(&binding.role))
                .with_hint(hint),
        ));
        let rules = access.rules.get(&binding.role).map(Vec::as_slice);
        let scope = match binding.at {
            Where::Everywhere => Scope::Cluster,
            Where::In(_) => Scope::Namespace,
        };
        let rules = rules.unwrap_or_default();
        for grant in rules::grants(rules, &access.gitops, scope, &label) {
            let mut boxed =
                SectionBox::new(grant.title, grant.lines, BoxLook::Plain).with_hint(grant.hint);
            if grant.capability.is_some() {
                boxed = boxed.with_block(GRANTED);
            }
            if grant.everything {
                boxed = boxed.with_blocks(vec![GRANTED; 6]);
            }
            let same = grants
                .iter_mut()
                .find(|g| g.boxed.title == boxed.title && g.boxed.lines == boxed.lines);
            match same {
                Some(same) => same.roles.push(at),
                None => grants.push(Grant {
                    boxed,
                    roles: vec![at],
                }),
            }
        }
    }
    if roles.is_empty() {
        roles.push(Placing::none(SectionBox::new(
            "no binding names it".into(),
            Vec::new(),
            BoxLook::Plain,
        )));
    }
    let of = |capability: Capability| {
        let title = capability.words();
        grants.iter().position(|g| g.boxed.title == title)
    };
    let everything = grants.iter().position(|g| g.boxed.blocks.len() == 6);
    let place = |capability: Capability| of(capability).or(everything);
    let follows = followed::follows(model, account, held, &place, look);
    Scene {
        runs_head: "runs as it",
        runs,
        account: boxed,
        roles,
        grants,
        follows,
    }
}

/// Each kind's name, by its API group and plural.
fn kind_names(model: &Model) -> HashMap<(&str, &str), &str> {
    let plurals = model.snapshot.plurals.iter();
    plurals
        .map(|((group, kind), plural)| ((group.as_str(), plural.as_str()), kind.as_str()))
        .collect()
}

/// `capabilities` in words, joined the way a sentence lists them.
pub fn listed(capabilities: &[Capability]) -> String {
    let words: Vec<String> = capabilities.iter().map(|c| c.words().to_string()).collect();
    and(&words)
}
