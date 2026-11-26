//! What an account gains by being able to exec into a pod or create a
//! similar one: the ServiceAccount the pod runs as, whether it mounts
//! that account's token, and the names of the Secrets it uses.

use k8s_openapi::api::core::v1::{EnvFromSource, EnvVar, Pod};
use transect_graph::tokens::projects_for_the_api;

/// One pod that has not finished, with the fields that `Access::build`
/// reads.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Running {
    pub namespace: String,
    pub name: String,
    /// The node it is scheduled on.
    pub node: Option<String>,
    /// The ServiceAccount it runs as.
    pub account: String,
    /// Whether it mounts a token of its ServiceAccount that the API
    /// server accepts.  It is read from the pod's projected volumes and
    /// not from `automountServiceAccountToken`, because the API server
    /// writes the token volume into the pod spec when that setting
    /// allows it.
    pub token: bool,
    /// The names of the Secrets that its volumes and environment
    /// variables use.
    pub secrets: Vec<String>,
}

impl Running {
    pub fn read(pod: &Pod) -> Option<Self> {
        if transect_graph::pods::finished(pod) {
            return None;
        }
        let spec = pod.spec.as_ref()?;
        let volumes = spec.volumes.iter().flatten();
        let mut token = false;
        let mut secrets: Vec<String> = Vec::new();
        for volume in volumes {
            if let Some(name) = volume.secret.as_ref().and_then(|s| s.secret_name.clone()) {
                secrets.push(name);
            }
            let sources = volume
                .projected
                .iter()
                .flat_map(|p| p.sources.iter().flatten());
            for source in sources {
                token |= source
                    .service_account_token
                    .as_ref()
                    .is_some_and(projects_for_the_api);
                if let Some(secret) = &source.secret {
                    secrets.push(secret.name.clone());
                }
            }
        }
        let containers = spec.containers.iter();
        for container in containers.chain(spec.init_containers.iter().flatten()) {
            secrets.extend(environment(&container.env, &container.env_from));
        }
        for container in spec.ephemeral_containers.iter().flatten() {
            secrets.extend(environment(&container.env, &container.env_from));
        }
        secrets.sort();
        secrets.dedup();
        Some(Self {
            namespace: pod.metadata.namespace.clone()?,
            name: pod.metadata.name.clone()?,
            node: spec.node_name.clone(),
            account: spec
                .service_account_name
                .clone()
                .unwrap_or_else(|| "default".into()),
            token,
            secrets,
        })
    }
}

/// The names of the Secrets that `env` and `env_from` read.
fn environment(env: &Option<Vec<EnvVar>>, env_from: &Option<Vec<EnvFromSource>>) -> Vec<String> {
    let keys = env.iter().flatten();
    let keys = keys.filter_map(|var| {
        let from = var.value_from.as_ref()?;
        Some(from.secret_key_ref.as_ref()?.name.clone())
    });
    let whole = env_from.iter().flatten();
    let whole = whole.filter_map(|from| Some(from.secret_ref.as_ref()?.name.clone()));
    keys.chain(whole).collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn pod(spec: &str) -> Pod {
        let text = format!("{{metadata: {{namespace: shop, name: web-1}}, spec: {spec}}}");
        serde_yaml_ng::from_str(&text).unwrap()
    }

    #[rstest]
    #[case::the_default_account("{containers: [{name: main}]}", Some(("default", false, vec![])))]
    #[case::a_mounted_token("{serviceAccountName: web, containers: [{name: main}], volumes: [{name: kube-api-access, projected: {sources: [{serviceAccountToken: {path: token}}]}}]}", Some(("web", true, vec![])))]
    #[case::a_token_for_another_audience("{serviceAccountName: relay, automountServiceAccountToken: false, containers: [{name: main}], volumes: [{name: chat, projected: {sources: [{serviceAccountToken: {path: token, audience: chat.example.test}}]}}]}", Some(("relay", false, vec![])))]
    #[case::a_token_for_the_api_server("{serviceAccountName: web, containers: [{name: main}], volumes: [{name: api, projected: {sources: [{serviceAccountToken: {path: token, audience: 'https://kubernetes.default.svc.cluster.local'}}]}}]}", Some(("web", true, vec![])))]
    #[case::an_ephemeral_containers_environment("{containers: [{name: main}], ephemeralContainers: [{name: debug, envFrom: [{secretRef: {name: db-login}}]}]}", Some(("default", false, vec!["db-login"])))]
    #[case::secrets_by_volume_and_environment("{containers: [{name: main, env: [{name: A, valueFrom: {secretKeyRef: {name: api-token, key: a}}}], envFrom: [{secretRef: {name: db-login}}]}], initContainers: [{name: setup, envFrom: [{secretRef: {name: api-token}}]}], volumes: [{name: tls, secret: {secretName: web-tls}}, {name: more, projected: {sources: [{secret: {name: extra}}]}}]}", Some(("default", false, vec!["api-token", "db-login", "extra", "web-tls"])))]
    fn reads_what_a_pod_runs_as_and_mounts(
        #[case] spec: &str,
        #[case] expected: Option<(&str, bool, Vec<&str>)>,
    ) {
        let found = Running::read(&pod(spec));
        let found = found.map(|r| (r.account, r.token, r.secrets));
        let expected =
            expected.map(|(a, t, s)| (a.to_string(), t, s.into_iter().map(String::from).collect()));
        assert_eq!(found, expected);
    }

    #[test]
    fn a_finished_pod_is_not_read() {
        let mut finished = pod("{containers: [{name: main}]}");
        finished.status = serde_yaml_ng::from_str("{phase: Succeeded}").unwrap();
        assert_eq!(Running::read(&finished), None);
    }
}
