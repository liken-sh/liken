//! Every word that the loss shows.  The rest of the text in these lines
//! is the name of a machine, a namespace, a workload, or a pod, a
//! count, or a time.

mod cascade;
mod next;
mod real;

use std::fmt;

/// What an application loses when its identity provider goes down, in
/// the cascade's lines and in the application's parcel tag.
pub const SIGN_IN: &str = "sign-in";

use super::sidebar::{Blind, Line, Named, Sidebar};
use super::{Lost, Parcel, State, Stranded, Vital};
use crate::placement::Eviction;
use crate::storage::Type;
use transect_graph::links::{Piece, listed};

impl fmt::Display for Lost {
    /// "alpha", "alpha and beta", "alpha, beta, and gamma".
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&list(self.names()))
    }
}

impl Lost {
    /// "it" for one machine and "they" for several, each with its verb,
    /// so each sentence agrees with the set of machines that it names.
    fn pronoun(&self) -> (&'static str, &'static str) {
        match self.0.len() {
            1 => ("it", "returns"),
            _ => ("they", "return"),
        }
    }
}

impl Sidebar {
    /// "If alpha were lost", or for machines really lost, "alpha is
    /// lost · 3 minutes", and for a prediction during a real loss,
    /// "If alpha were lost too".
    pub fn title(&self) -> String {
        let supposed = list(self.supposed());
        match (self.all_real(), self.real()) {
            (true, _) => self.lost_text(),
            (false, true) => format!("If {supposed} were lost too"),
            (false, false) => format!("If {supposed} were lost"),
        }
    }

    /// "12 pods run there.", or "12 pods were running there." when every
    /// machine is really lost.
    pub fn summary(&self) -> String {
        match (self.pods, self.all_real()) {
            (1, false) => "1 pod runs there.".into(),
            (pods, false) => format!("{pods} pods run there."),
            (1, true) => "1 pod was running there.".into(),
            (pods, true) => format!("{pods} pods were running there."),
        }
    }

    /// "Down until it returns", or "until they return" for several.
    pub fn stuck_heading(&self) -> String {
        let (pronoun, verb) = self.machines.pronoun();
        format!("Down until {pronoun} {verb}")
    }

    /// The heading of the workloads whose pods start on other machines
    /// after Kubernetes evicts them: "Down for about 5 minutes, then back
    /// elsewhere", with the time when Kubernetes evicts most of the pods
    /// on the lost machines.
    pub fn back_heading(&self) -> String {
        match self.next.usual {
            Some(Eviction::After(seconds)) if seconds > 0 => {
                format!("Down for about {}, then back elsewhere", duration(seconds))
            }
            Some(Eviction::After(_)) => "Down until replaced elsewhere".into(),
            Some(Eviction::Never) | None => "Down until evicted, then back elsewhere".into(),
        }
    }

    pub fn vital_heading(&self) -> &'static str {
        "Affects the whole cluster"
    }

    pub fn short_heading(&self) -> &'static str {
        "Running short"
    }

    pub fn primary_heading(&self) -> &'static str {
        "No writes until a replica is promoted"
    }

    /// The text that the sidebar shows in place of the stuck, back,
    /// primary, and short lists when all four are empty.
    pub fn nothing_text(&self) -> Option<&'static str> {
        let nothing = self.stuck.is_empty()
            && self.back.is_empty()
            && self.primary.is_empty()
            && self.short.is_empty();
        nothing.then_some("Nothing goes down or runs short.")
    }

    /// "and 4 more namespaces one replica short".
    pub fn folded_text(&self) -> Option<String> {
        match self.folded {
            0 => None,
            1 => Some("and 1 more namespace one replica short".into()),
            count => Some(format!("and {count} more namespaces one replica short")),
        }
    }

    /// "3 DaemonSets' pods are lost with it".
    pub fn daemon_sets_text(&self) -> Option<String> {
        let (pronoun, _) = self.machines.pronoun();
        match self.daemon_sets {
            0 => None,
            1 => Some(format!(
                "1 DaemonSet's pods are lost with {}",
                object(pronoun)
            )),
            count => Some(format!(
                "{count} DaemonSets' pods are lost with {}",
                object(pronoun)
            )),
        }
    }
}

fn object(pronoun: &str) -> &str {
    match pronoun {
        "they" => "them",
        other => other,
    }
}

impl fmt::Display for Parcel {
    /// The parcel's tag: "db down · web 1/2", or "down" and "1/2" for a
    /// namespace with one workload, because the parcel's name already
    /// identifies it.  Then come the down workloads' `provides` texts
    /// from intent, "no order storage", and then the workloads in other
    /// namespaces that its workloads cannot reach: "can't reach db and
    /// cache".
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let mut tags: Vec<String> = self
            .taken
            .iter()
            .map(|(name, state)| {
                let what = match state {
                    State::Down { .. } => "down".to_string(),
                    State::Short { ready, wanted } => format!("{ready}/{wanted}"),
                    State::Primary { .. } => "no primary".to_string(),
                };
                match self.alone {
                    true => what,
                    false => format!("{name} {what}"),
                }
            })
            .collect();
        if !self.lacks.is_empty() {
            tags.push(format!(
                "no {}",
                list(self.lacks.iter().map(String::as_str))
            ));
        }
        if !self.loses.is_empty() {
            tags.push(format!(
                "can't reach {}",
                list(self.loses.iter().map(String::as_str))
            ));
        }
        f.write_str(&tags.join(" · "))
    }
}

impl fmt::Display for Blind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&transect_graph::links::text(&self.pieces()))
    }
}

impl Blind {
    /// What follows the namespace's name.
    pub fn pieces(&self) -> Vec<Piece> {
        let what = match self.vital {
            Vital::Alerting => "no alert reaches anyone, so nothing below pages",
            Vital::Dns => "pods cannot look up names, in the cluster or out of it",
            Vital::WayIn => "web traffic from outside the cluster stops reaching it",
        };
        vec![self.name.piece(), Piece::Words(format!(": {what}"))]
    }
}

impl fmt::Display for Line {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&transect_graph::links::text(&self.pieces()))
    }
}

impl Line {
    /// What follows the namespace's name: "web, its only instance",
    /// "web and cache, one replica each", "queue, none of 3 left", or
    /// "web and api 2 of 3".  Workloads with the same phrase share it.
    pub fn pieces(&self) -> Vec<Piece> {
        let mut phrases: Vec<(Phrase, Vec<&Named>)> = Vec::new();
        for (named, state) in &self.workloads {
            let phrase = Phrase::of(*state);
            match phrases.iter_mut().find(|(seen, _)| *seen == phrase) {
                Some((_, names)) => names.push(named),
                None => phrases.push((phrase, vec![named])),
            }
        }
        let mut pieces = Vec::new();
        for (at, (phrase, names)) in phrases.into_iter().enumerate() {
            if at > 0 {
                pieces.push(Piece::words("; "));
            }
            pieces.extend(phrase.with(&names));
        }
        pieces
    }
}

impl Line {
    /// Why the line's stranded workloads stay down, one sentence per
    /// reason: "Its volume is attached to alpha." when the line names
    /// one workload, and "Their volumes are attached to alpha." when
    /// every workload in the line has the same reason.  Otherwise the
    /// sentence names each group: "db and web: their volumes are
    /// attached to alpha. radio holds usb radio-1 on alpha."
    ///
    /// Each workload that it names is its own piece, so a page links
    /// each name and never searches the words for one.
    pub fn stranded_pieces(&self) -> Vec<Piece> {
        let mut groups: Vec<(&Stranded, Vec<&str>)> = Vec::new();
        for (name, reason) in &self.stranded {
            match groups.iter_mut().find(|(seen, _)| *seen == reason) {
                Some((_, names)) => names.push(name),
                None => groups.push((reason, vec![name])),
            }
        }
        let named = |name: &str| {
            let found = self.workloads.iter().find(|(named, _)| named.shown == name);
            found.map_or_else(|| Piece::words(name), |(named, _)| named.piece())
        };
        let whole = groups.len() == 1 && self.stranded.len() == self.workloads.len();
        let mut pieces = Vec::new();
        for (at, (reason, names)) in groups.iter().enumerate() {
            if at > 0 {
                pieces.push(Piece::words(" "));
            }
            match (whole, names.as_slice()) {
                (true, [_]) => pieces.push(Piece::words(reason.sentence(None))),
                (true, _) => pieces.push(Piece::words(reason.plural())),
                (false, [name]) => {
                    let said = reason.sentence(Some(NAME));
                    let (before, after) = said.split_once(NAME).unwrap_or((&said, ""));
                    pieces.push(Piece::words(before));
                    pieces.push(named(name));
                    pieces.push(Piece::words(after));
                }
                (false, names) => {
                    let plural = reason.plural();
                    let mut rest = plural.chars();
                    let first = rest.next().map(|c| c.to_lowercase().to_string());
                    pieces.extend(listed(names.iter().map(|name| vec![named(name)]).collect()));
                    pieces.push(Piece::words(format!(
                        ": {}{}",
                        first.unwrap_or_default(),
                        rest.as_str()
                    )));
                }
            }
        }
        pieces.retain(|piece| !matches!(piece, Piece::Words(words) if words.is_empty()));
        pieces
    }
}

/// A marker for the position of a workload's name in a sentence about
/// it.  `stranded_pieces` puts the workload's own piece in its place.
const NAME: &str = "\u{0}";

impl Stranded {
    /// Why one workload stays down, about `name`, or about "it" when
    /// the line names only the one workload.
    fn sentence(&self, name: Option<&str>) -> String {
        let (its, it, it_object) = match name {
            Some(name) => (format!("{name}'s"), name.to_string(), name.to_string()),
            None => ("Its".into(), "It".into(), "it".into()),
        };
        match self {
            Self::Disk { machine } => format!("{its} volume is on {machine}'s own disk."),
            Self::Attached { kind, machine } => {
                format!("{its} {}volume is attached to {machine}.", kind_word(kind))
            }
            Self::Device { device, machine } => format!("{it} holds {device} on {machine}."),
            Self::NoFit => format!("No other machine fits {it_object}."),
            Self::StatefulSet { machine } => format!(
                "{it} is a StatefulSet's pod, which is not replaced until {machine} confirms it gone."
            ),
            Self::NeverEvicted => {
                format!("{its} pods tolerate a lost machine, so they are never evicted.")
            }
        }
    }

    /// Why several workloads stay down for one reason, about "them".
    fn plural(&self) -> String {
        match self {
            Self::Disk { machine } => format!("Their volumes are on {machine}'s own disk."),
            Self::Attached { kind, machine } => {
                format!(
                    "Their {}volumes are attached to {machine}.",
                    kind_word(kind)
                )
            }
            Self::Device { device, machine } => format!("They hold {device} on {machine}."),
            Self::NoFit => "No other machine fits them.".into(),
            Self::StatefulSet { machine } => format!(
                "They are StatefulSets' pods, which are not replaced until {machine} confirms them gone."
            ),
            Self::NeverEvicted => {
                "Their pods tolerate a lost machine, so they are never evicted.".into()
            }
        }
    }
}

/// The kind of volume, as the text before "volume": "iSCSI ", "fast ",
/// or nothing when the kind gives the reader no useful information.
fn kind_word(kind: &Type) -> String {
    match kind {
        Type::Iscsi => "iSCSI ".to_string(),
        Type::Class(class) => format!("{class} "),
        Type::Nfs | Type::Unclassed | Type::Local => String::new(),
    }
}

/// The phrase that a line shows for the workloads in one state.
#[derive(PartialEq)]
enum Phrase {
    Only,
    NoneLeft(i32),
    Of(i32, i32),
    /// The primary lost, with this many replicas left to promote.
    Primary(i32),
}

impl Phrase {
    fn of(state: State) -> Self {
        match state {
            State::Down { wanted: 1 } => Self::Only,
            State::Down { wanted } => Self::NoneLeft(wanted),
            State::Short { ready, wanted } => Self::Of(ready, wanted),
            State::Primary { ready, .. } => Self::Primary(ready),
        }
    }

    /// The workloads `names`, then this phrase's text about them.
    fn with(&self, names: &[&Named]) -> Vec<Piece> {
        let mut pieces = listed(names.iter().map(|named| vec![named.piece()]).collect());
        let said = match (self, names.len()) {
            (Self::Only, 1) => ", its only instance".to_string(),
            (Self::Only, _) => ", one replica each".to_string(),
            (Self::NoneLeft(wanted), _) => format!(", none of {wanted} left"),
            (Self::Of(ready, wanted), _) => format!(" {ready} of {wanted}"),
            (Self::Primary(1), 1) => "'s primary; its operator promotes its replica".to_string(),
            (Self::Primary(_), 1) => {
                "'s primary; its operator promotes one of its replicas".to_string()
            }
            (Self::Primary(_), _) => {
                "'s primaries; their operator promotes a replica of each".to_string()
            }
        };
        pieces.push(Piece::Words(said));
        pieces
    }
}

/// A time, rounded as a reader would round it: "30 seconds", "1 minute",
/// "5 minutes".  A time past two minutes rounds to whole minutes.
fn duration(seconds: i64) -> String {
    match seconds {
        1 => "1 second".into(),
        60 => "1 minute".into(),
        s if s % 60 == 0 || s >= 120 => format!("{} minutes", (s + 30) / 60),
        s => format!("{s} seconds"),
    }
}

/// "a", "a and b", "a, b, and c".
fn list<'a>(names: impl IntoIterator<Item = &'a str>) -> String {
    let names: Vec<&str> = names.into_iter().collect();
    match names.as_slice() {
        [] => String::new(),
        [one] => (*one).into(),
        [first, second] => format!("{first} and {second}"),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
    }
}

#[cfg(test)]
mod tests;
