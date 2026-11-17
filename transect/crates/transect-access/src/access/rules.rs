//! What a role's rules grant, in the short text that an account's
//! drawing puts in a box.  Each of the six capabilities that a rule
//! grants gets a box with the capability's name and the verbs and kinds
//! that grant it.  A rule with every verb on every kind gives one box
//! for the whole role.  Every other rule gets a box from a template of
//! its verbs and kinds: "read" for `get`, `list`, and `watch`, and the
//! verb, or "write", for the verbs that change objects.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::rbac::v1::PolicyRule;

use super::capability::{self, ALL, Capability, GitOpsKind, Scope, allows};

/// The verbs that only read.
const READS: [&str; 3] = ["get", "list", "watch"];

/// The most kinds a box names before it counts the rest.
const NAMED: usize = 2;

/// The most boxes that a role draws for rules that change objects.  The
/// rest share one box.
const WRITES: usize = 4;

/// One box of what a role grants.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Grant {
    /// The capability that the box shows, when it is one of the six.
    pub capability: Option<Capability>,
    /// Whether it is every verb on every kind.
    pub everything: bool,
    pub title: String,
    pub lines: Vec<String>,
    /// The rules that the box describes, shown on hover.
    pub hint: String,
}

/// A kind as a box names it, from its API group and plural: "Jobs".
pub type Label<'a> = &'a dyn Fn(&str, &str) -> String;

/// What `rules` grant through a binding of `scope`, one box for each
/// grant.
pub fn grants(
    rules: &[PolicyRule],
    gitops: &[GitOpsKind],
    scope: Scope,
    label: Label,
) -> Vec<Grant> {
    if rules.iter().any(capability::everything) {
        return vec![Grant {
            capability: None,
            everything: true,
            title: "every verb, every kind".into(),
            lines: Vec::new(),
            hint: "Every verb on every resource in every API group.".into(),
        }];
    }
    let mut found: Vec<Grant> = ALL
        .iter()
        .filter_map(|capability| granted(rules, *capability, gitops, scope, label))
        .collect();
    found.extend(others(rules, gitops, scope, label));
    found
}

/// The box of `capability`, when a rule grants it.
fn granted(
    rules: &[PolicyRule],
    capability: Capability,
    gitops: &[GitOpsKind],
    scope: Scope,
    label: Label,
) -> Option<Grant> {
    let matched: Vec<(&PolicyRule, Vec<String>)> = rules
        .iter()
        .filter_map(|rule| Some((rule, capability::grants(rule, capability, gitops, scope)?)))
        .collect();
    if matched.is_empty() {
        return None;
    }
    // The kinds that each set of verbs applies to, in the order that
    // the capability names them.
    let mut by_verbs: Vec<(Vec<&str>, Vec<String>)> = Vec::new();
    let mut hint: Vec<String> = Vec::new();
    let mut notes: Vec<String> = Vec::new();
    for ask in capability::asked(capability, gitops, scope) {
        let (group, resource) = (ask.group, ask.resource);
        let allowed: Vec<&str> = ask
            .verbs
            .into_iter()
            .filter(|verb| {
                let mut rules = matched.iter();
                rules.any(|(rule, _)| allows(rule, verb, &group, &resource))
            })
            .collect();
        if allowed.is_empty() {
            continue;
        }
        let in_group = match group.as_str() {
            "" => String::new(),
            group => format!(" in {group}"),
        };
        hint.push(format!("{} on {resource}{in_group}", allowed.join(", ")));
        if let Some(note) = ask.note {
            notes.push(note.into());
        }
        let kind = labelled(&group, &resource, label);
        match by_verbs.iter_mut().find(|(verbs, _)| *verbs == allowed) {
            Some((_, kinds)) => kinds.push(kind),
            None => by_verbs.push((allowed, vec![kind])),
        }
    }
    let said: Vec<String> = by_verbs
        .iter()
        .map(|(verbs, kinds)| format!("{} {}", verbs.join(", "), listed(kinds)))
        .collect();
    let mut line = said.join("; ");
    let unnamed = matched.iter().any(|(_, names)| names.is_empty());
    if !unnamed {
        let names: BTreeSet<&String> = matched.iter().flat_map(|(_, names)| names).collect();
        let names: Vec<&str> = names.into_iter().map(String::as_str).collect();
        line = format!("{line}, by name: {}", names.join(", "));
        let lists = matched.iter().any(|(rule, _)| {
            ["list", "watch"]
                .iter()
                .any(|verb| allows(rule, verb, "", "secrets"))
        });
        if capability == Capability::ReadSecrets && !lists {
            line.push_str("; it may not list them");
        }
    }
    let mut lines = vec![line];
    lines.extend(notes);
    Some(Grant {
        capability: Some(capability),
        everything: false,
        title: capability.words().into(),
        lines,
        hint: format!("{}.", hint.join("; ")),
    })
}

/// A resource as a box names it: "Pods", or "Pods exec" for a
/// subresource.
fn labelled(group: &str, resource: &str, label: Label) -> String {
    match resource.split_once('/') {
        Some((resource, sub)) => format!("{} {sub}", label(group, resource)),
        None => label(group, resource),
    }
}

/// The kinds that a rule names, each as a box names it, except the
/// kinds that the six capabilities cover for this rule, because the
/// capabilities' own boxes name those.  Every subresource shows,
/// because a subresource such as `nodes/proxy` may give access to more
/// than its kind.  The only exception is a kind's `status` beside the
/// kind itself in a rule that only reads, because reading the kind
/// reads its status.
fn kinds(rule: &PolicyRule, gitops: &[GitOpsKind], scope: Scope, label: Label) -> Vec<String> {
    let reads = rule.verbs.iter().all(|verb| READS.contains(&verb.as_str()));
    let covered: BTreeSet<(String, String)> = ALL
        .iter()
        .filter(|capability| capability::grants(rule, **capability, gitops, scope).is_some())
        .flat_map(|capability| capability::asked(*capability, gitops, scope))
        .map(|ask| (ask.group, ask.resource))
        .collect();
    let resources = rule.resources.iter().flatten();
    let status_beside_kind = |resource: &str| {
        let kind = resource.strip_suffix("/status");
        let mut named = resources.clone();
        kind.is_some_and(|kind| named.any(|other| other == kind))
    };
    let mut found: Vec<String> = Vec::new();
    for group in rule.api_groups.iter().flatten() {
        for resource in rule.resources.iter().flatten() {
            if covered.contains(&(group.clone(), resource.clone()))
                || (reads && status_beside_kind(resource))
            {
                continue;
            }
            let kind = match (group.as_str(), resource.as_str()) {
                ("*", "*") => "every kind".to_string(),
                (group, "*") => format!("every kind in {}", group_name(group)),
                (group, resource) => labelled(group, resource, label),
            };
            if !found.contains(&kind) {
                found.push(kind);
            }
        }
    }
    // A rule on every kind of several groups gets one entry that counts
    // the groups, in place of an entry for each group.
    let groups = found
        .iter()
        .filter(|kind| kind.starts_with(EVERY_KIND_IN))
        .count();
    if groups > 1 {
        found.retain(|kind| !kind.starts_with(EVERY_KIND_IN));
        found.push(format!("every kind in {groups} API groups"));
    }
    found
}

const EVERY_KIND_IN: &str = "every kind in ";

fn group_name(group: &str) -> &str {
    match group {
        "" => "the core group",
        group => group,
    }
}

/// The boxes for every rule that is none of the six capabilities.  The
/// kinds that the rules change get a box for each set of verbs, and the
/// kinds that the rules only read share one box.
fn others(rules: &[PolicyRule], gitops: &[GitOpsKind], scope: Scope, label: Label) -> Vec<Grant> {
    let mut writes: BTreeMap<Vec<String>, Vec<String>> = BTreeMap::new();
    let mut reads: Vec<String> = Vec::new();
    for rule in rules {
        let kinds = kinds(rule, gitops, scope, label);
        if kinds.is_empty() {
            continue;
        }
        let changes: Vec<String> = rule
            .verbs
            .iter()
            .filter(|verb| !READS.contains(&verb.as_str()))
            .cloned()
            .collect();
        let into = match changes.is_empty() {
            true => &mut reads,
            false => writes.entry(changes).or_default(),
        };
        for kind in kinds {
            if !into.contains(&kind) {
                into.push(kind);
            }
        }
    }
    let mut found: Vec<Grant> = writes
        .iter()
        .map(|(verbs, kinds)| write(verbs, kinds))
        .collect();
    found.sort_by(|a, b| (&a.title, &a.lines).cmp(&(&b.title, &b.lines)));
    if found.len() > WRITES {
        let rest = found.split_off(WRITES - 1);
        let hints: Vec<String> = rest.iter().map(|grant| grant.hint.clone()).collect();
        found.push(Grant {
            capability: None,
            everything: false,
            title: "write".into(),
            lines: vec![format!("{} more sets of kinds", rest.len())],
            hint: hints.join(" "),
        });
    }
    if !reads.is_empty() {
        found.push(Grant {
            capability: None,
            everything: false,
            title: "read".into(),
            lines: vec![listed(&reads)],
            hint: format!("get, list, or watch on {}.", reads.join(", ")),
        });
    }
    found
}

/// The box of the kinds that `verbs` change.  Its title is the verb,
/// such as "patch", or "write" for several verbs.  Its kinds go under
/// the title in the same form as in the "read" box, and the verbs show
/// on hover.
fn write(verbs: &[String], kinds: &[String]) -> Grant {
    let word = match verbs {
        [one] if one != "*" => one.clone(),
        _ => "write".into(),
    };
    let verbs = match verbs {
        [one] if one == "*" => "every verb".to_string(),
        verbs => verbs.join(", "),
    };
    Grant {
        capability: None,
        everything: false,
        title: word,
        lines: vec![listed(kinds)],
        hint: format!("{verbs} on {}.", kinds.join(", ")),
    }
}

/// `kinds` named in a line: the first few, and how many more.
fn listed(kinds: &[String]) -> String {
    match kinds.len() > NAMED + 1 {
        true => format!(
            "{}, and {} more kinds",
            kinds[..NAMED].join(", "),
            kinds.len() - NAMED
        ),
        false => kinds.join(", "),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn rules(text: &str) -> Vec<PolicyRule> {
        serde_yaml::from_str(text).unwrap()
    }

    fn label(_: &str, plural: &str) -> String {
        let mut chars = plural.chars();
        let first = chars.next().map(|c| c.to_ascii_uppercase());
        first.into_iter().chain(chars).collect()
    }

    fn said(text: &str) -> Vec<String> {
        said_through(text, Scope::Cluster)
    }

    fn said_through(text: &str, scope: Scope) -> Vec<String> {
        let gitops = vec![("apply.example.test".to_string(), "trees".to_string())];
        let found = grants(&rules(text), &gitops, scope, &label);
        found
            .into_iter()
            .map(|grant| format!("{}: {}", grant.title, grant.lines.join(" | ")))
            .collect()
    }

    #[rstest]
    #[case::every_verb("[{apiGroups: ['*'], resources: ['*'], verbs: ['*']}]", &["every verb, every kind: "])]
    #[case::create_jobs("[{apiGroups: [batch], resources: [jobs], verbs: [create, get]}]", &["run pods: create Jobs"])]
    #[case::patch_workloads(
        "[{apiGroups: [apps], resources: [deployments, statefulsets], verbs: [get, patch]}]",
        &["change workloads: patch Deployments, Statefulsets"]
    )]
    #[case::secrets_by_name(
        "[{apiGroups: [''], resources: [secrets], verbs: [get], resourceNames: [api-token]}]",
        &["read Secrets: get Secrets, by name: api-token; it may not list them"]
    )]
    #[case::an_applier("[{apiGroups: [apply.example.test], resources: [trees], verbs: [patch, delete]}]", &["change what a GitOps tool applies: patch Trees"])]
    #[case::one_verb_on_two_kinds(
        "[{apiGroups: [example.test], resources: [checks, checks/status], verbs: [get, patch]}]",
        &["patch: Checks, Checks status"]
    )]
    #[case::several_verbs(
        "[{apiGroups: [''], resources: [configmaps], verbs: [list, create, patch, delete]}]",
        &["write: Configmaps"]
    )]
    #[case::only_reads(
        "[{apiGroups: [''], resources: [nodes, namespaces], verbs: [get, list]}, {apiGroups: [batch], resources: ['*'], verbs: [watch]}]",
        &["read: Nodes, Namespaces, every kind in batch"]
    )]
    #[case::every_kind_of_several_groups(
        "[{apiGroups: [a.example.test, b.example.test], resources: ['*'], verbs: [patch]}]",
        &["patch: every kind in 2 API groups"]
    )]
    #[case::a_capability_and_more(
        "[{apiGroups: [''], resources: [pods, services], verbs: [create]}]",
        &["run pods: create Pods", "create: Services"]
    )]
    #[case::the_kubelets_api(
        "[{apiGroups: [''], resources: [nodes, nodes/proxy, nodes/metrics], verbs: [get]}]",
        &["exec into pods: get Nodes proxy | the kubelet's API runs commands in any pod on its machine", "read: Nodes, Nodes metrics"]
    )]
    #[case::a_status_beside_its_kind(
        "[{apiGroups: [example.test], resources: [checks, checks/status], verbs: [get]}]",
        &["read: Checks"]
    )]
    #[case::a_status_alone(
        "[{apiGroups: [example.test], resources: [checks/status], verbs: [get]}]",
        &["read: Checks status"]
    )]
    fn says_what_a_role_grants(#[case] text: &str, #[case] expected: &[&str]) {
        assert_eq!(said(text), expected);
    }

    #[test]
    fn names_a_write_by_its_verb_with_the_verbs_on_hover() {
        let text =
            "[{apiGroups: [''], resources: [configmaps], verbs: [list, create, patch, delete]}]";
        let found = grants(&rules(text), &[], Scope::Cluster, &label);
        let hints: Vec<&str> = found.iter().map(|grant| grant.hint.as_str()).collect();
        assert_eq!(hints, ["create, patch, delete on Configmaps."]);
    }

    #[test]
    fn says_a_cluster_scoped_rule_through_a_rolebinding_reaches_only_its_kind() {
        let text = "[{apiGroups: [''], resources: [nodes/proxy], verbs: [get]}]";
        assert_eq!(said_through(text, Scope::Namespace), ["read: Nodes proxy"]);
    }
}
