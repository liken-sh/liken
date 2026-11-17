//! The usage queries that a `MetricsSource` declares: what each machine
//! and each container uses, and the labels that identify the machine or
//! the container.  Which exporter writes these series, and which label
//! its scrape configuration puts a `Node`'s name in, differ from one
//! cluster to the next, so the cluster declares the queries and the
//! labels, and transect's code does not contain them.

use serde::Deserialize;

/// The usage that a cluster declares how to read.  Each query may be
/// left out, and the page then shows the figure that it would return
/// as missing.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Queries {
    /// The labels that may name the `Node` of a machine's series, tried
    /// in order.
    pub node_labels: Vec<String>,
    /// Cores each machine uses.
    pub machine_cpu: Option<String>,
    /// Bytes each machine uses.
    pub machine_memory: Option<String>,
    /// Cores each container uses.
    pub container_cpu: Option<ContainerQuery>,
    /// Bytes each container uses.
    pub container_memory: Option<ContainerQuery>,
}

/// A query that returns one series per container, and the labels in
/// each series that hold its namespace, pod, and name.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ContainerQuery {
    pub query: String,
    pub labels: ContainerLabels,
}

#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ContainerLabels {
    pub namespace: String,
    pub pod: String,
    pub container: String,
}

/// A query that returns one series per machine.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MachineQuery {
    query: String,
}

impl Queries {
    /// The queries as declared, or why they do not validate.
    pub fn read(
        node_labels: Vec<String>,
        machine_cpu: Option<MachineQuery>,
        machine_memory: Option<MachineQuery>,
        container_cpu: Option<ContainerQuery>,
        container_memory: Option<ContainerQuery>,
    ) -> Result<Self, String> {
        let queries = Self {
            node_labels,
            machine_cpu: machine_cpu.map(|query| query.query),
            machine_memory: machine_memory.map(|query| query.query),
            container_cpu,
            container_memory,
        };
        let machines = [
            ("machineCpu", &queries.machine_cpu),
            ("machineMemory", &queries.machine_memory),
        ];
        for (name, query) in machines {
            if query
                .as_deref()
                .is_some_and(|query| query.trim().is_empty())
            {
                return Err(format!("queries.{name}.query is empty"));
            }
        }
        let names_machines = queries.machine_cpu.is_some() || queries.machine_memory.is_some();
        if names_machines && queries.node_labels.is_empty() {
            return Err("nodeLabels names no label, so no series matches a machine".into());
        }
        if queries
            .node_labels
            .iter()
            .any(|label| label.trim().is_empty())
        {
            return Err("nodeLabels names an empty label".into());
        }
        let containers = [
            ("containerCpu", &queries.container_cpu),
            ("containerMemory", &queries.container_memory),
        ];
        for (name, query) in containers {
            let Some(query) = query else { continue };
            if query.query.trim().is_empty() {
                return Err(format!("queries.{name}.query is empty"));
            }
            let ContainerLabels {
                namespace,
                pod,
                container,
            } = &query.labels;
            if [namespace, pod, container]
                .iter()
                .any(|label| label.trim().is_empty())
            {
                return Err(format!("queries.{name}.labels names an empty label"));
            }
        }
        Ok(queries)
    }

    /// Whether the cluster declares no usage query at all.
    pub fn is_empty(&self) -> bool {
        self.machine_cpu.is_none()
            && self.machine_memory.is_none()
            && self.container_cpu.is_none()
            && self.container_memory.is_none()
    }
}
