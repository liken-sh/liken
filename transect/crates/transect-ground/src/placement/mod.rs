//! Whether a pod may run on a machine, by the checks that read only the
//! pod's spec and the `Node`: the node selector, the required node
//! affinity, and the node's taints against the pod's tolerations.  The
//! DaemonSet controller makes the same checks before it creates a pod,
//! so `transect-map`'s namespace matrix uses `skip` to show why a
//! `DaemonSet` runs no pod on a machine by design.

mod eviction;

use std::collections::BTreeMap;

use k8s_openapi::api::core::v1::{
    Node, NodeSelectorRequirement, NodeSelectorTerm, PodSpec, Taint, Toleration,
};

pub use eviction::{Eviction, eviction, lost_taint};

/// The taint that Kubernetes puts on a cordoned machine.  The scheduler
/// treats a `Node` with `spec.unschedulable` set as if it had this
/// taint, whether or not the taint is on the `Node` yet.
const UNSCHEDULABLE: &str = "node.kubernetes.io/unschedulable";

/// The reason that a `DaemonSet`'s pod does not run on a machine.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Skip {
    NodeSelector,
    Affinity,
    /// The key of the first taint the template does not tolerate.
    Taint(String),
}

/// The keys of the taints that the DaemonSet controller adds
/// tolerations for on every pod it creates, even when the template
/// lists none.  So a `DaemonSet`'s pod can run on a machine that is
/// cordoned, under pressure, or not ready.
const TOLERATED_BY_DAEMON_SETS: [&str; 7] = [
    "node.kubernetes.io/not-ready",
    "node.kubernetes.io/unreachable",
    "node.kubernetes.io/disk-pressure",
    "node.kubernetes.io/memory-pressure",
    "node.kubernetes.io/pid-pressure",
    "node.kubernetes.io/unschedulable",
    "node.kubernetes.io/network-unavailable",
];

/// The reason that the DaemonSet controller does not create a pod of
/// `template` for `node`, or `None` when the pod fits there.
pub fn skip(template: &PodSpec, node: &Node) -> Option<Skip> {
    check(template, node, &TOLERATED_BY_DAEMON_SETS)
}

/// Whether the scheduler can place a pod of `spec` on `node`.  Unlike
/// the DaemonSet controller, the scheduler adds no tolerations to the
/// pod, so a cordoned machine fits only a pod that tolerates the taint
/// of a cordon.
pub fn fits(spec: &PodSpec, node: &Node) -> bool {
    let cordoned = node.spec.as_ref().and_then(|s| s.unschedulable) == Some(true);
    let cordon = Taint {
        key: UNSCHEDULABLE.into(),
        effect: "NoSchedule".into(),
        ..Taint::default()
    };
    let tolerations = spec.tolerations.as_deref().unwrap_or_default();
    if cordoned && !tolerations.iter().any(|t| tolerates(t, &cordon)) {
        return false;
    }
    check(spec, node, &[]).is_none()
}

/// Returns the first of the three checks that fails, in the order node
/// selector, required node affinity, taints.  A taint whose key is in
/// `exempt` is tolerated whatever the template lists.
fn check(template: &PodSpec, node: &Node, exempt: &[&str]) -> Option<Skip> {
    let no_labels = BTreeMap::new();
    let labels = node.metadata.labels.as_ref().unwrap_or(&no_labels);
    let name = node.metadata.name.as_deref().unwrap_or_default();
    let selector = template.node_selector.iter().flatten();
    if !selector.into_iter().all(|(k, v)| labels.get(k) == Some(v)) {
        return Some(Skip::NodeSelector);
    }
    let required = template
        .affinity
        .as_ref()
        .and_then(|affinity| affinity.node_affinity.as_ref())
        .and_then(|node_affinity| {
            node_affinity
                .required_during_scheduling_ignored_during_execution
                .as_ref()
        });
    if let Some(required) = required {
        let terms = &required.node_selector_terms;
        if !terms.iter().any(|term| term_matches(term, labels, name)) {
            return Some(Skip::Affinity);
        }
    }
    let tolerations = template.tolerations.as_deref().unwrap_or_default();
    let taints = node.spec.as_ref().and_then(|spec| spec.taints.as_ref());
    taints
        .into_iter()
        .flatten()
        .filter(|taint| matches!(taint.effect.as_str(), "NoSchedule" | "NoExecute"))
        .filter(|taint| !exempt.contains(&taint.key.as_str()))
        .find(|taint| !tolerations.iter().any(|t| tolerates(t, taint)))
        .map(|taint| Skip::Taint(taint.key.clone()))
}

/// A term matches when every label expression and every field
/// requirement matches.  The API treats an empty term as matching no
/// node.  The only field that a requirement can match is
/// `metadata.name`.
fn term_matches(term: &NodeSelectorTerm, labels: &BTreeMap<String, String>, name: &str) -> bool {
    let expressions = term.match_expressions.as_deref().unwrap_or_default();
    let fields = term.match_fields.as_deref().unwrap_or_default();
    if expressions.is_empty() && fields.is_empty() {
        return false;
    }
    let fields_hold = fields.iter().all(|field| {
        let value = (field.key == "metadata.name").then_some(name);
        requirement_holds(field, value)
    });
    let expressions_hold = expressions.iter().all(|expression| {
        let value = labels.get(&expression.key).map(String::as_str);
        requirement_holds(expression, value)
    });
    fields_hold && expressions_hold
}

/// Whether a node's label or field `value` satisfies `requirement`.
/// `value` is `None` when the node has no such label.  `Gt` and `Lt`
/// compare `value` with the requirement's first value as integers, and
/// fail when either is not an integer.
fn requirement_holds(requirement: &NodeSelectorRequirement, value: Option<&str>) -> bool {
    let values = requirement.values.as_deref().unwrap_or_default();
    let number = |text: &str| text.parse::<i64>().ok();
    let compare = |ordering: std::cmp::Ordering| {
        let bound = values.first().and_then(|bound| number(bound));
        match (value.and_then(number), bound) {
            (Some(value), Some(bound)) => value.cmp(&bound) == ordering,
            _ => false,
        }
    };
    match requirement.operator.as_str() {
        "In" => value.is_some_and(|value| values.iter().any(|v| v == value)),
        "NotIn" => value.is_none_or(|value| !values.iter().any(|v| v == value)),
        "Exists" => value.is_some(),
        "DoesNotExist" => value.is_none(),
        "Gt" => compare(std::cmp::Ordering::Greater),
        "Lt" => compare(std::cmp::Ordering::Less),
        _ => false,
    }
}

/// Whether `toleration` tolerates `taint`, by the API's rule.  An empty
/// effect tolerates every effect, `Exists` with an empty key tolerates
/// every taint, and `Equal`, the default, requires the key and the value
/// to match.
pub fn tolerates(toleration: &Toleration, taint: &Taint) -> bool {
    let effect = toleration.effect.as_deref().unwrap_or_default();
    if !effect.is_empty() && effect != taint.effect {
        return false;
    }
    let key = toleration.key.as_deref().unwrap_or_default();
    match toleration.operator.as_deref().unwrap_or("Equal") {
        "Exists" => key.is_empty() || key == taint.key,
        "Equal" => {
            let value = toleration.value.as_deref().unwrap_or_default();
            key == taint.key && value == taint.value.as_deref().unwrap_or_default()
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn template(yaml: &str) -> PodSpec {
        serde_yaml::from_str(&format!("{{containers: [], {yaml}}}")).unwrap()
    }

    /// A machine named `web-1` with `labels` and `taints`, as YAML.
    fn node(labels: &str, taints: &str) -> Node {
        serde_yaml::from_str(&format!(
            "metadata: {{name: web-1, labels: {labels}}}\nspec: {{taints: {taints}}}"
        ))
        .unwrap()
    }

    fn affinity(expressions: &str) -> String {
        format!(
            "affinity: {{nodeAffinity: {{requiredDuringSchedulingIgnoredDuringExecution: {{nodeSelectorTerms: [{expressions}]}}}}}}"
        )
    }

    #[rstest]
    #[case::no_requirements("hostNetwork: false", "{}", "[]", None)]
    #[case::selector_matches("nodeSelector: {disk: ssd}", "{disk: ssd}", "[]", None)]
    #[case::selector_does_not_match(
        "nodeSelector: {disk: ssd}",
        "{disk: hdd}",
        "[]",
        Some(Skip::NodeSelector)
    )]
    #[case::not_in_without_the_label(
        &affinity("{matchExpressions: [{key: radio, operator: NotIn, values: [none]}]}"),
        "{}",
        "[]",
        None
    )]
    #[case::not_in_with_the_label(
        &affinity("{matchExpressions: [{key: radio, operator: NotIn, values: [none]}]}"),
        "{radio: none}",
        "[]",
        Some(Skip::Affinity)
    )]
    #[case::either_term(
        &affinity("{matchExpressions: [{key: gpu, operator: Exists}]}, {matchExpressions: [{key: disk, operator: In, values: [ssd]}]}"),
        "{disk: ssd}",
        "[]",
        None
    )]
    #[case::greater_than(
        &affinity("{matchExpressions: [{key: cores, operator: Gt, values: ['4']}]}"),
        "{cores: '2'}",
        "[]",
        Some(Skip::Affinity)
    )]
    #[case::by_name(
        &affinity("{matchFields: [{key: metadata.name, operator: In, values: [web-1]}]}"),
        "{}",
        "[]",
        None
    )]
    #[case::empty_term_matches_nothing(&affinity("{}"), "{}", "[]", Some(Skip::Affinity))]
    #[case::untolerated_taint(
        "hostNetwork: false",
        "{}",
        "[{key: dedicated, value: screen, effect: NoSchedule}]",
        Some(Skip::Taint("dedicated".into()))
    )]
    #[case::preferred_taint_does_not_skip(
        "hostNetwork: false",
        "{}",
        "[{key: dedicated, value: screen, effect: PreferNoSchedule}]",
        None
    )]
    #[case::tolerated_by_value(
        "tolerations: [{key: dedicated, value: screen, effect: NoSchedule}]",
        "{}",
        "[{key: dedicated, value: screen, effect: NoSchedule}]",
        None
    )]
    #[case::wrong_value(
        "tolerations: [{key: dedicated, value: other}]",
        "{}",
        "[{key: dedicated, value: screen, effect: NoSchedule}]",
        Some(Skip::Taint("dedicated".into()))
    )]
    #[case::tolerates_everything(
        "tolerations: [{operator: Exists}]",
        "{}",
        "[{key: dedicated, value: screen, effect: NoExecute}]",
        None
    )]
    #[case::cordoned_machine_is_tolerated_by_a_daemon_set(
        "hostNetwork: false",
        "{}",
        "[{key: node.kubernetes.io/unschedulable, effect: NoSchedule}]",
        None
    )]
    fn skip_returns_the_first_failed_check(
        #[case] spec: &str,
        #[case] labels: &str,
        #[case] taints: &str,
        #[case] expected: Option<Skip>,
    ) {
        assert_eq!(skip(&template(spec), &node(labels, taints)), expected);
    }

    fn cordoned(taints: &str) -> Node {
        let mut node = node("{}", taints);
        node.spec.as_mut().unwrap().unschedulable = Some(true);
        node
    }

    #[rstest]
    #[case::anywhere("hostNetwork: false", node("{}", "[]"), true)]
    #[case::selector_does_not_match("nodeSelector: {disk: ssd}", node("{}", "[]"), false)]
    #[case::untolerated_taint(
        "hostNetwork: false",
        node("{}", "[{key: dedicated, value: screen, effect: NoSchedule}]"),
        false
    )]
    #[case::not_ready_taint_with_a_toleration_of_another_effect(
        "tolerations: [{key: node.kubernetes.io/not-ready, operator: Exists, effect: NoExecute, tolerationSeconds: 300}]",
        node("{}", "[{key: node.kubernetes.io/not-ready, effect: NoSchedule}]"),
        false
    )]
    #[case::cordoned_without_its_taint_yet("hostNetwork: false", cordoned("[]"), false)]
    #[case::cordoned_but_tolerated(
        "tolerations: [{key: node.kubernetes.io/unschedulable, operator: Exists}]",
        cordoned("[{key: node.kubernetes.io/unschedulable, effect: NoSchedule}]"),
        true
    )]
    fn the_scheduler_adds_no_tolerations_to_a_pod(
        #[case] spec: &str,
        #[case] node: Node,
        #[case] fits_there: bool,
    ) {
        assert_eq!(fits(&template(spec), &node), fits_there);
    }
}
