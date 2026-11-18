//! Kubernetes label selector matching, with the semantics the API server
//! applies to `matchLabels` and `matchExpressions`.

use std::collections::BTreeMap;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::{LabelSelector, LabelSelectorRequirement};

/// Whether `labels` satisfy every term of `selector`.  An empty selector
/// matches everything, as it does in the API.
pub fn matches(selector: &LabelSelector, labels: &BTreeMap<String, String>) -> bool {
    let labels_match = selector
        .match_labels
        .iter()
        .flatten()
        .all(|(key, value)| labels.get(key) == Some(value));
    let expressions_match = selector
        .match_expressions
        .iter()
        .flatten()
        .all(|requirement| satisfies(requirement, labels));
    labels_match && expressions_match
}

fn satisfies(requirement: &LabelSelectorRequirement, labels: &BTreeMap<String, String>) -> bool {
    let value = labels.get(&requirement.key);
    let values = requirement.values.as_deref().unwrap_or_default();
    match requirement.operator.as_str() {
        "In" => value.is_some_and(|value| values.contains(value)),
        "NotIn" => value.is_none_or(|value| !values.contains(value)),
        "Exists" => value.is_some(),
        "DoesNotExist" => value.is_none(),
        _ => false,
    }
}

/// Rejects a selector that the API server would reject. A `Layers`
/// document with such a selector is then left out with the reason
/// `Invalid`, so a typo is reported instead of matching no namespace.
pub fn validate(selector: &LabelSelector) -> Result<(), String> {
    for requirement in selector.match_expressions.iter().flatten() {
        let has_values = requirement.values.as_ref().is_some_and(|v| !v.is_empty());
        match (requirement.operator.as_str(), has_values) {
            ("In" | "NotIn", true) | ("Exists" | "DoesNotExist", false) => {}
            ("In" | "NotIn", false) => {
                return Err(format!(
                    "the expression on label {} uses {}, which needs at least one value",
                    requirement.key, requirement.operator
                ));
            }
            ("Exists" | "DoesNotExist", true) => {
                return Err(format!(
                    "the expression on label {} uses {}, which takes no values",
                    requirement.key, requirement.operator
                ));
            }
            (operator, _) => {
                return Err(format!(
                    "the expression on label {} has the operator {operator:?}, which must be In, NotIn, Exists, or DoesNotExist",
                    requirement.key
                ));
            }
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn labels(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        pairs
            .iter()
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect()
    }

    fn match_labels(pairs: &[(&str, &str)]) -> LabelSelector {
        LabelSelector {
            match_labels: Some(labels(pairs)),
            ..Default::default()
        }
    }

    fn expression(key: &str, operator: &str, values: &[&str]) -> LabelSelector {
        LabelSelector {
            match_expressions: Some(vec![LabelSelectorRequirement {
                key: key.into(),
                operator: operator.into(),
                values: (!values.is_empty())
                    .then(|| values.iter().map(|v| v.to_string()).collect()),
            }]),
            ..Default::default()
        }
    }

    #[rstest]
    #[case::empty_selector_matches_all(LabelSelector::default(), &[("tier", "web")], true)]
    #[case::match_labels_equal(match_labels(&[("tier", "web")]), &[("tier", "web"), ("x", "y")], true)]
    #[case::match_labels_differs(match_labels(&[("tier", "web")]), &[("tier", "db")], false)]
    #[case::match_labels_missing(match_labels(&[("tier", "web")]), &[], false)]
    #[case::match_labels_needs_all(match_labels(&[("tier", "web"), ("team", "a")]), &[("tier", "web")], false)]
    #[case::in_listed(expression("tier", "In", &["web", "db"]), &[("tier", "db")], true)]
    #[case::in_unlisted(expression("tier", "In", &["web", "db"]), &[("tier", "cache")], false)]
    #[case::in_missing(expression("tier", "In", &["web"]), &[], false)]
    #[case::not_in_listed(expression("tier", "NotIn", &["web"]), &[("tier", "web")], false)]
    #[case::not_in_unlisted(expression("tier", "NotIn", &["web"]), &[("tier", "db")], true)]
    #[case::not_in_missing(expression("tier", "NotIn", &["web"]), &[], true)]
    #[case::exists_present(expression("tier", "Exists", &[]), &[("tier", "")], true)]
    #[case::exists_missing(expression("tier", "Exists", &[]), &[], false)]
    #[case::does_not_exist_present(expression("tier", "DoesNotExist", &[]), &[("tier", "web")], false)]
    #[case::does_not_exist_missing(expression("tier", "DoesNotExist", &[]), &[], true)]
    fn selector_matching(
        #[case] selector: LabelSelector,
        #[case] namespace_labels: &[(&str, &str)],
        #[case] expected: bool,
    ) {
        assert_eq!(matches(&selector, &labels(namespace_labels)), expected);
    }

    #[rstest]
    #[case::in_with_values(expression("tier", "In", &["web"]), true)]
    #[case::in_without_values(expression("tier", "In", &[]), false)]
    #[case::exists_without_values(expression("tier", "Exists", &[]), true)]
    #[case::exists_with_values(expression("tier", "Exists", &["web"]), false)]
    #[case::unknown_operator(expression("tier", "Equals", &["web"]), false)]
    fn selector_validation(#[case] selector: LabelSelector, #[case] valid: bool) {
        assert_eq!(validate(&selector).is_ok(), valid);
    }
}
