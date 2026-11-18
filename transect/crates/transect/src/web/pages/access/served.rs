//! The port boxes of a workload's access drawing: the ports its
//! containers declare, and the ports that a way in from outside the
//! cluster or a `NetworkPolicy` rule refers to by number or by name.

use transect_access::access::drawing::reach::Port;
use transect_access::access::ways::Target;
use transect_graph::edges::Object;
use transect_map::map::WorkloadContents;
use transect_network::policy::Ports;

/// The ports that the containers of a workload declare, sorted by
/// number. A port that something refers to and no container declares is
/// added after them.
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

    /// The index of the port that `target` refers to. When no container
    /// declares it, the port is added to the list.
    pub fn of(&mut self, target: &Target) -> usize {
        let found = self
            .ports
            .iter()
            .position(|(number, name, _)| match target {
                Target::Number(wanted) => number == wanted,
                Target::Name(wanted) => name.as_deref() == Some(wanted.as_str()),
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

    /// The sorted indexes of the listed ports that `ports` allows. For a
    /// span that contains none of them, the span's first port is added to
    /// the list. `Ports::Any` returns no indexes.
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
