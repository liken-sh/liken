//! The sidebar and machine texts for machines that are really lost, and
//! for the control plane that a loss leaves.

use super::{duration, list};
use crate::loss::Counted;
use crate::loss::sidebar::Sidebar;
use crate::machines::Machine;

impl Sidebar {
    /// The breadcrumb after the cluster's: "alpha is lost" when every
    /// machine of the set is really lost, otherwise "if alpha were lost"
    /// for the predicted machines, with "too" when some are really lost.
    pub fn crumb(&self) -> String {
        match (self.all_real(), self.real()) {
            (true, _) => self.lost_text_short(),
            (false, true) => format!("if {} were lost too", list(self.supposed())),
            (false, false) => format!("if {} were lost", list(self.supposed())),
        }
    }

    /// "alpha is lost · 3 minutes", timed from the machine that has been
    /// lost the longest.
    pub fn lost_text(&self) -> String {
        match self.longest() {
            Some(seconds) => format!("{} · {}", self.lost_text_short(), since(seconds)),
            None => self.lost_text_short(),
        }
    }

    /// "alpha is lost", "alpha and beta are lost".
    fn lost_text_short(&self) -> String {
        let names = list(self.gone.iter().map(|g| g.machine.as_str()));
        match self.gone.len() {
            1 => format!("{names} is lost"),
            _ => format!("{names} are lost"),
        }
    }

    /// "beta is already lost · 3 minutes.", for a prediction made while
    /// some machines are really lost.
    pub fn gone_text(&self) -> Option<String> {
        if self.all_real() || !self.real() {
            return None;
        }
        let names = list(self.gone.iter().map(|g| g.machine.as_str()));
        let verb = if self.gone.len() == 1 { "is" } else { "are" };
        Some(match self.longest() {
            Some(seconds) => format!("{names} {verb} already lost · {}.", since(seconds)),
            None => format!("{names} {verb} already lost."),
        })
    }

    /// "2 of 3 etcd members remain, which keeps a majority.", or from
    /// the control plane, "2 of 3 control-plane machines remain, which
    /// keeps a majority (a guess)."
    pub fn majority_text(&self) -> Option<String> {
        let majority = self.majority?;
        let (remain, of) = (majority.remain, majority.of);
        let verb = if remain == 1 { "remains" } else { "remain" };
        let which = if majority.has_quorum() {
            "which keeps a majority"
        } else {
            "which loses the majority"
        };
        Some(match majority.counted {
            Counted::Etcd => format!("{remain} of {of} etcd members {verb}, {which}."),
            Counted::ControlPlane => {
                format!("{remain} of {of} control-plane machines {verb}, {which} (a guess).")
            }
        })
    }

    /// Whether the majority line counts control-plane machines in place
    /// of etcd members, so the page draws it fainter as a guess.
    pub fn majority_guessed(&self) -> bool {
        self.majority
            .is_some_and(|majority| majority.counted == Counted::ControlPlane)
    }

    /// The sidebar card, above the lists of workloads, that explains a
    /// control plane without a majority.
    pub fn headless_text(&self) -> Option<String> {
        if !self.headless() {
            return None;
        }
        let (pronoun, verb) = self.machines.pronoun();
        let machines = &self.machines;
        Some(format!(
            "With no majority, the API accepts no writes, so nothing is evicted or scheduled again. \
             Everything that ran on {machines} is down until {pronoun} {verb}; \
             everything else keeps running, and nothing reconciles it."
        ))
    }

    /// The majority line's `title` tooltip, which says how the line
    /// counts the members.
    pub fn majority_title(&self) -> &'static str {
        match self.majority.map(|majority| majority.counted) {
            Some(Counted::Etcd) => {
                "The API does not list etcd's members, so this counts the machines with the node-role.kubernetes.io/etcd label."
            }
            _ => {
                "The API does not list etcd's members, so this counts each control-plane machine as one."
            }
        }
    }
}

impl Machine {
    /// The tag beside a lost machine's name on the machines band: "if
    /// lost" for a predicted loss, or "Lost 3m" for a real one.
    pub fn lost_tag(&self) -> String {
        match (self.really_lost, self.lost_minutes) {
            (false, _) => "if lost".into(),
            (true, Some(minutes)) => format!("Lost {minutes}m"),
            (true, None) => "Lost".into(),
        }
    }

    /// The pod count under a lost machine's name on the machines band:
    /// "12 pods run here", or "12 pods were here" for a real loss.
    pub fn lost_text(&self) -> String {
        match (self.pods, self.really_lost) {
            (1, false) => "1 pod runs here".into(),
            (pods, false) => format!("{pods} pods run here"),
            (1, true) => "1 pod was here".into(),
            (pods, true) => format!("{pods} pods were here"),
        }
    }
}

/// How long a machine has been lost: "under a minute", "3 minutes".
fn since(seconds: i64) -> String {
    match seconds {
        s if s < 60 => "under a minute".into(),
        s => duration(s / 60 * 60),
    }
}

#[cfg(test)]
mod tests;
