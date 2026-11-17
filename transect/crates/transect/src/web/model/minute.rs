//! What a model derives as of one minute on the clock: health, the log
//! findings, the restarts too old to count, and the firing alerts.
//! Each reads the time only through `health::minutes_between`, which
//! counts the minute boundaries crossed, so any time within one minute
//! gives the same, and every render in that minute reads one copy.

use std::collections::HashMap;

use k8s_openapi::jiff::Timestamp;

use crate::web::snapshot::Snapshot;
use transect_observe::alerts::Firing;
use transect_observe::health::Health;
use transect_supply::findings::{Finding, Restarted};

pub struct Minute {
    pub health: HashMap<String, Health>,
    /// The findings of `findings::worth_knowing`, and one for the
    /// machines' own error lines.
    pub logged: Vec<Finding>,
    pub restarted: Vec<Restarted>,
    pub firing: Vec<Firing>,
}

impl Minute {
    pub fn read(snapshot: &Snapshot, now: Timestamp) -> Self {
        Self {
            health: snapshot.health(now),
            logged: snapshot.log_findings(now),
            restarted: snapshot.earlier_restarts(now),
            firing: snapshot.firing(now),
        }
    }

    /// The minute `now` is in, which keys what a model keeps.
    pub fn of(now: Timestamp) -> i64 {
        now.as_second().div_euclid(60)
    }
}
