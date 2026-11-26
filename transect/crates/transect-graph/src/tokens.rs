//! The audiences of the `ServiceAccount` tokens that a pod or a pod
//! template mounts through projected volumes.  A program that receives
//! such a token checks it with a `TokenReview` for its own audience, so
//! an audience says which program the pod can call as its account.
//! The access lens reads them from running pods, and the edges of the
//! agent system read them from the templates that pods are made from.

use k8s_openapi::api::core::v1::{PodSpec, ServiceAccountTokenProjection};
use serde_json::Value;

/// The audiences that the API server accepts by default: the issuer
/// URL that kubeadm and k3s configure, with and without
/// `.cluster.local`.  A token for any other audience does not
/// authenticate at the API server.  A projection with no audience gets
/// the API server's own, so `for_the_api` accepts it too.
const API_AUDIENCES: [&str; 2] = [
    "https://kubernetes.default.svc",
    "https://kubernetes.default.svc.cluster.local",
];

/// Whether the API server accepts a token for `audience`.
pub fn for_the_api(audience: Option<&str>) -> bool {
    match audience {
        None | Some("") => true,
        Some(audience) => API_AUDIENCES.contains(&audience),
    }
}

/// Whether the API server accepts the token that `projection` mounts.
pub fn projects_for_the_api(projection: &ServiceAccountTokenProjection) -> bool {
    for_the_api(projection.audience.as_deref())
}

/// The audiences of the tokens that `spec` mounts, sorted and without
/// the API server's.
pub fn audiences(spec: &PodSpec) -> Vec<String> {
    let volumes = spec.volumes.iter().flatten();
    let projected = volumes.filter_map(|volume| volume.projected.as_ref());
    let sources = projected.flat_map(|projected| projected.sources.iter().flatten());
    let tokens = sources.filter_map(|source| source.service_account_token.as_ref());
    collect(tokens.map(|token| token.audience.as_deref()))
}

/// The audiences of the tokens that `template`, a pod template in the
/// form of a `PodTemplateSpec`, mounts at
/// `spec.volumes[].projected.sources[].serviceAccountToken.audience`.
/// The template can be a part of one, as an operator's custom resource
/// keeps it raw and merges it into a pod of its own, so a field that is
/// missing or of another type gives nothing.
pub fn template_audiences(template: &Value) -> Vec<String> {
    fn list(value: Option<&Value>) -> impl Iterator<Item = &Value> {
        value.and_then(Value::as_array).into_iter().flatten()
    }
    let volumes = list(template.pointer("/spec/volumes"));
    let sources = volumes.flat_map(|volume| list(volume.pointer("/projected/sources")));
    let tokens = sources.filter_map(|source| source.get("serviceAccountToken"));
    collect(tokens.map(|token| token.get("audience").and_then(Value::as_str)))
}

/// The labels at `metadata.labels` of `template`, a pod template in the
/// form of a `PodTemplateSpec`.
pub fn template_labels(template: &Value) -> Vec<(String, String)> {
    let labels = template
        .pointer("/metadata/labels")
        .and_then(Value::as_object);
    let labels = labels.into_iter().flatten();
    let text = labels.filter_map(|(key, value)| Some((key.clone(), value.as_str()?.to_string())));
    text.collect()
}

fn collect<'a>(audiences: impl Iterator<Item = Option<&'a str>>) -> Vec<String> {
    let mut found: Vec<String> = audiences
        .filter(|audience| !for_the_api(*audience))
        .flatten()
        .map(String::from)
        .collect();
    found.sort();
    found.dedup();
    found
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::no_volumes("{containers: [{name: main}]}", vec![])]
    #[case::the_api_servers_token(
        "{containers: [{name: main}], volumes: [{name: api, projected: {sources: [{serviceAccountToken: {path: token}}]}}]}",
        vec![]
    )]
    #[case::the_api_servers_named_audience(
        "{containers: [{name: main}], volumes: [{name: api, projected: {sources: [{serviceAccountToken: {path: token, audience: 'https://kubernetes.default.svc'}}]}}]}",
        vec![]
    )]
    #[case::two_audiences_in_two_volumes(
        "{containers: [{name: main}], volumes: [{name: b, projected: {sources: [{serviceAccountToken: {path: t, audience: vault}}]}}, {name: a, projected: {sources: [{secret: {name: x}}, {serviceAccountToken: {path: t, audience: chat}}]}}]}",
        vec!["chat", "vault"]
    )]
    fn reads_the_audiences_a_pod_spec_mounts(#[case] spec: &str, #[case] expected: Vec<&str>) {
        let spec: PodSpec = serde_yaml_ng::from_str(spec).unwrap();
        assert_eq!(audiences(&spec), expected);
    }

    #[rstest]
    #[case::a_partial_template_with_no_spec("{metadata: {labels: {a: b}}}", vec![])]
    #[case::volumes_of_the_wrong_type("{spec: {volumes: {name: x}}}", vec![])]
    #[case::one_token_twice(
        "{spec: {volumes: [{name: a, projected: {sources: [{serviceAccountToken: {audience: vault}}]}}, {name: b, projected: {sources: [{serviceAccountToken: {audience: vault}}]}}]}}",
        vec!["vault"]
    )]
    #[case::a_token_for_the_api_server(
        "{spec: {volumes: [{name: a, projected: {sources: [{serviceAccountToken: {path: token}}]}}]}}",
        vec![]
    )]
    fn reads_the_audiences_a_raw_template_mounts(
        #[case] template: &str,
        #[case] expected: Vec<&str>,
    ) {
        let template: Value = serde_yaml_ng::from_str(template).unwrap();
        assert_eq!(template_audiences(&template), expected);
    }

    #[rstest]
    #[case::labels("{metadata: {labels: {app: web, tier: '2'}}}", vec![("app", "web"), ("tier", "2")])]
    #[case::no_metadata("{spec: {}}", vec![])]
    fn reads_the_labels_of_a_raw_template(
        #[case] template: &str,
        #[case] expected: Vec<(&str, &str)>,
    ) {
        let template: Value = serde_yaml_ng::from_str(template).unwrap();
        let expected: Vec<(String, String)> = expected
            .into_iter()
            .map(|(k, v)| (k.into(), v.into()))
            .collect();
        assert_eq!(template_labels(&template), expected);
    }
}
