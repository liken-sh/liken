//! The words of each entry in the list of changes.

use k8s_openapi::jiff::Timestamp;
use k8s_openapi::jiff::tz::TimeZone;

use super::{Change, Moved, Restart, What};
use crate::forge::Commit;
use transect_graph::edges::Object;
use transect_observe::health::times;

impl Change {
    /// "10:09", in UTC, or "Sun 22:13" for a change on another day
    /// than `now`'s.
    pub fn time(&self, now: Timestamp) -> String {
        let at = self.at.to_zoned(TimeZone::UTC);
        let today = now.to_zoned(TimeZone::UTC).date() == at.date();
        match today {
            true => at.strftime("%H:%M").to_string(),
            false => at.strftime("%a %H:%M").to_string(),
        }
    }

    /// The entry's sentence.
    pub fn text(&self) -> String {
        match &self.what {
            What::Rolled {
                workload,
                revision: Some(revision),
            } => format!("{} rolled out revision {revision}", named(workload)),
            What::Rolled {
                workload,
                revision: None,
            } => format!("{} rolled out a new revision", named(workload)),
            What::Ran { job } => format!("Job {} ran", named(job)),
            What::Scheduled {
                cron_job,
                succeeded,
            } => {
                let how = match succeeded {
                    Some(true) => "and succeeded",
                    Some(false) => "and did not succeed",
                    None => "and is running",
                };
                format!("{} ran on its schedule {how}", named(cron_job))
            }
            What::Applied {
                source,
                reference,
                appliers,
                ..
            } => applied(source.as_ref(), reference, appliers),
            What::Restarted { workload, restarts } => restarted(workload, restarts),
            What::Node { node, moved } => node_text(node, moved),
            What::Alert { name, about, count } => {
                let count = match count {
                    1 => String::new(),
                    count => format!(", {count} alerts"),
                };
                format!("alert {name} started firing: {about}{count}")
            }
        }
    }

    /// The text of a line under a rollout.
    pub fn after_text(&self) -> String {
        match &self.what {
            What::Restarted { restarts, .. } => {
                let found = restarts
                    .iter()
                    .map(|restart| format!("then {}", one(restart)));
                found.collect::<Vec<_>>().join("; ")
            }
            _ => self.text(),
        }
    }

    /// The link to the commit that an applied revision came from.
    pub fn commit(&self) -> Option<(&str, &str)> {
        match &self.what {
            What::Applied {
                commit: Some(Commit::Link { href, text }),
                ..
            } => Some((href, text)),
            _ => None,
        }
    }
}

fn named(object: &Object) -> String {
    transect_graph::links::name_in(object, None)
}

/// "ops/shop applied main", or "41 Trees applied main from ops/fleet",
/// named by the appliers' kind.
fn applied(source: Option<&Object>, reference: &str, appliers: &[Object]) -> String {
    let reference = match reference {
        "" => "a commit",
        reference => reference,
    };
    let kind = match appliers {
        [first, rest @ ..] if rest.iter().all(|other| other.kind == first.kind) => &first.kind,
        _ => "applier",
    };
    match (appliers, source) {
        ([one], _) => format!("{} applied {reference}", named(one)),
        (all, Some(source)) => format!(
            "{} applied {reference} from {}",
            times(all.len() as i64, kind),
            named(source)
        ),
        (all, None) => format!("{} applied {reference}", times(all.len() as i64, kind)),
    }
}

/// "web in web-0 restarted (Error, exit 1)".
fn one(restart: &Restart) -> String {
    let why = match (&restart.reason, restart.exit_code) {
        (Some(reason), Some(code)) => format!(" ({reason}, exit {code})"),
        (Some(reason), None) => format!(" ({reason})"),
        (None, Some(code)) => format!(" (exit {code})"),
        (None, None) => String::new(),
    };
    format!("{} in {} restarted{why}", restart.container, restart.pod)
}

/// "shop/web: web in web-0 restarted (Error, exit 1)", or, for several
/// restarts, their count and the latest one.
fn restarted(workload: &Object, restarts: &[Restart]) -> String {
    let Some(latest) = restarts.last() else {
        return format!("{}: a container restarted", named(workload));
    };
    match restarts.len() {
        1 => format!("{}: {}", named(workload), one(latest)),
        count => {
            let mut pods: Vec<&str> = restarts.iter().map(|r| r.pod.as_str()).collect();
            pods.sort();
            pods.dedup();
            format!(
                "{}: {} restarted in {}, the latest {}",
                named(workload),
                times(count as i64, "container"),
                times(pods.len() as i64, "pod"),
                one(latest)
            )
        }
    }
}

/// "machine-a became ready; DiskPressure and MemoryPressure became
/// False", or "machine-a: MemoryPressure became True:
/// KubeletHasInsufficientMemory" for one condition alone.
fn node_text(node: &str, moved: &[Moved]) -> String {
    let ready = moved.iter().find(|m| m.condition == "Ready");
    let ready = ready.map(|m| {
        let reason = m.reason.as_ref().map(|r| format!(": {r}"));
        match m.status.as_str() {
            "True" => format!("{node} became ready"),
            _ => format!("{node} stopped being ready{}", reason.unwrap_or_default()),
        }
    });
    let others: Vec<&Moved> = moved.iter().filter(|m| m.condition != "Ready").collect();
    let mut statuses: Vec<&str> = others.iter().map(|m| m.status.as_str()).collect();
    statuses.sort();
    statuses.dedup();
    let said: Vec<String> = statuses
        .iter()
        .map(|status| {
            let these: Vec<&&Moved> = others.iter().filter(|m| m.status == *status).collect();
            let names: Vec<&str> = these.iter().map(|m| m.condition.as_str()).collect();
            let reason = match these.as_slice() {
                [one] => one.reason.as_ref().map(|r| format!(": {r}")),
                _ => None,
            };
            format!(
                "{} became {status}{}",
                listed(&names),
                reason.unwrap_or_default()
            )
        })
        .collect();
    match (ready, said.is_empty()) {
        (Some(ready), true) => ready,
        (Some(ready), false) => format!("{ready}; {}", said.join("; ")),
        (None, _) => format!("{node}: {}", said.join("; ")),
    }
}

/// "a", "a and b", or "a, b, and c".
fn listed(names: &[&str]) -> String {
    match names {
        [] => String::new(),
        [one] => (*one).into(),
        [one, two] => format!("{one} and {two}"),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
    }
}
