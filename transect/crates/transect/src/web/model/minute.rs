//! The values that a model derives from its snapshot and the time:
//! health, log findings, earlier restarts, and firing alerts. Each
//! reads the time only through `health::minutes_between`, which counts
//! the minute boundaries crossed. Every `now` within one minute gives
//! the same values, so the model computes them once for that minute and
//! every render in it reads the same copy.

use std::collections::HashMap;

use k8s_openapi::jiff::Timestamp;

use crate::web::snapshot::Snapshot;
use transect_observe::alerts::Firing;
use transect_observe::health::Health;
use transect_supply::findings::{Finding, Restarted};

pub struct Minute {
    pub health: HashMap<String, Health>,
    /// The findings of `findings::worth_knowing`, plus one finding when
    /// any machine logged error lines of its own.
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

    /// The number of whole minutes from the Unix epoch to `now`. The model
    /// uses it as the key of the `Minute` it caches.
    pub fn of(now: Timestamp) -> i64 {
        now.as_second().div_euclid(60)
    }
}
