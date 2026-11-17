//! The ports a workload serves, by number or by the name a container
//! gives one, as its drawing's port boxes.

use transect_access::access::drawing::reach::Port;
use transect_access::access::ways::Target;
use transect_graph::edges::Object;
use transect_map::map::WorkloadContents;
use transect_network::policy::Ports;

/// The ports a workload's pods declare.  The list grows when a way in
/// or a rule names a port that no container declares.
pub struct Served {
    ports: Vec<(i32, Option<String>, String)>,
}

impl Served {
    pub fn read(contents: &WorkloadContents, object: &Object) -> Self {
        let workload = contents.workloads.get(object);
        let mut ports: Vec<(i32, Option<String>, String)> = Vec::new();
        for spec in workload.templates() {
            for container in &spec.containers {
                for port in container.ports.iter().flatten() {
                    if ports
                        .iter()
                        .any(|(number, ..)| *number == port.container_port)
                    {
                        continue;
                    }
                    let mut title = format!("Container port {}", port.container_port);
                    if let Some(name) = &port.name {
                        title.push_str(&format!(", {name}"));
                    }
                    if let Some(host) = port.host_port {
                        title.push_str(&format!(", and host port {host}"));
                    }
                    title.push_str(&format!(", in container {}.", container.name));
                    ports.push((port.container_port, port.name.clone(), title));
                }
            }
        }
        ports.sort_by_key(|(number, ..)| *number);
        Self { ports }
    }

    /// The place of the port `target` names, added when no container
    /// declares it.
    pub fn of(&mut self, target: &Target) -> usize {
        let found = self
            .ports
            .iter()
            .position(|(number, name, _)| match target {
                Target::Number(asked) => number == asked,
                Target::Name(asked) => name.as_deref() == Some(asked.as_str()),
            });
        found.unwrap_or_else(|| {
            let (number, name) = match target {
                Target::Number(number) => (*number, None),
                Target::Name(name) => (0, Some(name.clone())),
            };
            let title = "A port no container of it declares.".to_string();
            self.ports.push((number, name, title));
            self.ports.len() - 1
        })
    }

    /// The places of the ports `ports` admit: each declared port in
    /// them, or each span's first port when none is declared.
    pub fn admitted(&mut self, ports: &Ports) -> Vec<usize> {
        let Ports::Listed(spans) = ports else {
            return Vec::new();
        };
        let mut found = Vec::new();
        for span in spans {
            let inside = self.ports.iter().enumerate().filter(|(_, (number, ..))| {
                (i32::from(span.first)..=i32::from(span.last)).contains(number)
            });
            let inside: Vec<usize> = inside.map(|(at, _)| at).collect();
            match inside.is_empty() {
                true => found.push(self.of(&Target::Number(i32::from(span.first)))),
                false => found.extend(inside),
            }
        }
        found.sort();
        found.dedup();
        found
    }

    pub fn into_ports(self) -> Vec<Port> {
        let port = |(number, name, title): (i32, Option<String>, String)| Port {
            text: match (number, name) {
                (0, Some(name)) => format!(":{name}"),
                (number, _) => format!(":{number}"),
            },
            title,
        };
        self.ports.into_iter().map(port).collect()
    }
}
