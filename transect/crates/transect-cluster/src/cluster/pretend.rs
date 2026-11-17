//! Machines a development build pretends are not ready, so the loss that
//! the map draws for a real outage can be seen and worked on without
//! taking a machine down.  `transect` changes only its own copy of each
//! `Node`, and writes nothing to the cluster.

use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, NodeCondition};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::Time;
use k8s_openapi::jiff::Timestamp;

/// The machines to pretend are not ready, and since when.
#[derive(Clone, Debug, Default)]
pub struct Pretend {
    machines: Arc<[String]>,
    since: Option<Timestamp>,
}

impl Pretend {
    /// Pretends `machines` went not ready at `since`.  Release builds
    /// pretend nothing, so only tests and debug builds make one.
    #[cfg(any(debug_assertions, test))]
    pub fn new(machines: Vec<String>, since: Timestamp) -> Self {
        Self {
            machines: machines.into(),
            since: Some(since),
        }
    }

    /// `node` as `transect` reads it.  For a machine it pretends about,
    /// its `Ready` condition is `Unknown` since the pretense began, as a
    /// kubelet that stops posting status leaves it.
    pub fn apply(&self, node: Arc<Node>) -> Arc<Node> {
        let named = node.metadata.name.as_ref();
        let (Some(name), Some(since)) = (named, self.since) else {
            return node;
        };
        if !self.machines.contains(name) {
            return node;
        }
        let mut node = Arc::unwrap_or_clone(node);
        let status = node.status.get_or_insert_with(Default::default);
        let conditions = status.conditions.get_or_insert_with(Vec::new);
        conditions.retain(|condition| condition.type_ != "Ready");
        conditions.push(NodeCondition {
            type_: "Ready".into(),
            status: "Unknown".into(),
            reason: Some("NodeStatusUnknown".into()),
            message: Some("--pretend-not-ready names this machine".into()),
            last_transition_time: Some(Time(since)),
            ..Default::default()
        });
        Arc::new(node)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn node(name: &str) -> Arc<Node> {
        Arc::new(
            serde_yaml::from_str(&format!(
                "{{metadata: {{name: {name}}}, status: {{conditions: [{{type: Ready, status: 'True'}}, {{type: DiskPressure, status: 'False'}}]}}}}"
            ))
            .unwrap(),
        )
    }

    /// Each of `node`'s conditions as its type and status.
    fn conditions(node: &Node) -> Vec<(String, String)> {
        let conditions = node.status.as_ref().unwrap().conditions.iter().flatten();
        conditions
            .map(|c| (c.type_.clone(), c.status.clone()))
            .collect()
    }

    fn pairs(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
        pairs
            .iter()
            .map(|(t, s)| (t.to_string(), s.to_string()))
            .collect()
    }

    #[rstest]
    #[case::pretended("alpha", &[("DiskPressure", "False"), ("Ready", "Unknown")])]
    #[case::left_alone("beta", &[("Ready", "True"), ("DiskPressure", "False")])]
    fn pretends_only_about_the_machines_it_names(
        #[case] name: &str,
        #[case] expected: &[(&str, &str)],
    ) {
        let pretend = Pretend::new(vec!["alpha".into()], Timestamp::UNIX_EPOCH);
        assert_eq!(conditions(&pretend.apply(node(name))), pairs(expected));
    }

    #[rstest]
    fn pretends_nothing_by_default() {
        let found = Pretend::default().apply(node("alpha"));
        assert_eq!(
            conditions(&found),
            pairs(&[("Ready", "True"), ("DiskPressure", "False")])
        );
    }
}
