//! A resource's conditions, in the shape a CRD's `status.conditions`
//! takes, so that when these resources become CRDs, `transect` can
//! write the same conditions to the cluster that the `/resources` page
//! lists.

use std::fmt;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status {
    True,
    False,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Condition {
    /// The condition's type, such as `Loaded`.
    pub kind: &'static str,
    pub status: Status,
    /// One CamelCase word a program can match, such as `Invalid`.
    pub reason: &'static str,
    pub message: String,
}

impl Condition {
    /// The document was read and is in use.
    pub fn loaded() -> Self {
        Self {
            kind: "Loaded",
            status: Status::True,
            reason: "Loaded",
            message: String::new(),
        }
    }

    /// The document was left out, for `reason`.
    pub fn left_out(reason: &'static str, message: impl Into<String>) -> Self {
        Self {
            kind: "Loaded",
            status: Status::False,
            reason,
            message: message.into(),
        }
    }

    /// The document names something the cluster does not hold, such as
    /// a selector that matches nothing.
    pub fn unmatched(message: impl Into<String>) -> Self {
        Self {
            kind: "Matched",
            status: Status::False,
            reason: "NoMatch",
            message: message.into(),
        }
    }

    /// A reference by value found more than one object that holds the
    /// value, so it draws none of them.
    pub fn ambiguous(message: impl Into<String>) -> Self {
        Self {
            kind: "Matched",
            status: Status::False,
            reason: "Ambiguous",
            message: message.into(),
        }
    }

    /// The document names a kind the cluster does not serve.
    pub fn not_served(message: impl Into<String>) -> Self {
        Self {
            kind: "Served",
            status: Status::False,
            reason: "NotServed",
            message: message.into(),
        }
    }

    /// The document writes something on an object it draws nothing
    /// from, such as a list of dependencies on a `Service`.
    pub fn unattached(message: impl Into<String>) -> Self {
        Self {
            kind: "Attached",
            status: Status::False,
            reason: "DrawsNothing",
            message: message.into(),
        }
    }

    /// The document names a layer the cluster's `Layers` does not
    /// declare, so that part of it is ignored.
    pub fn unknown_layer(message: impl Into<String>) -> Self {
        Self {
            kind: "LayerDeclared",
            status: Status::False,
            reason: "UnknownLayer",
            message: message.into(),
        }
    }

    /// Two documents set the same thing, and the message states which one
    /// is shown.
    pub fn conflict(message: impl Into<String>) -> Self {
        Self {
            kind: "Conflict",
            status: Status::True,
            reason: "SetTwice",
            message: message.into(),
        }
    }

    /// The log store refused or missed the resource's last search.
    pub fn search_failed(message: impl Into<String>) -> Self {
        Self {
            kind: "Searched",
            status: Status::False,
            reason: "SearchFailed",
            message: message.into(),
        }
    }

    /// Whether the condition is one a reader should act on.
    pub fn trouble(&self) -> bool {
        match (self.kind, self.reason) {
            ("Loaded" | "Matched" | "Served" | "LayerDeclared" | "Attached" | "Searched", _) => {
                self.status == Status::False
            }
            _ => self.status == Status::True,
        }
    }

    /// The condition in words, then its message: "loaded", "not
    /// loaded: no spec".  The type, status, and reason stay in the
    /// line's title for a reader matching them by program.
    pub fn text(&self) -> String {
        let said = match (self.kind, self.status) {
            ("Loaded", Status::True) => "loaded",
            ("Loaded", Status::False) => "not loaded",
            ("Matched", Status::False) if self.reason == "Ambiguous" => "matches more than one",
            ("Matched", Status::False) => "matches nothing",
            ("Served", Status::False) => "names a kind the cluster does not serve",
            ("Attached", Status::False) => "draws nothing",
            ("LayerDeclared", Status::False) => "names a layer Layers does not declare",
            ("Conflict", Status::True) => "set twice",
            ("Searched", Status::False) => "search failed",
            (_, Status::True) => "true",
            (_, Status::False) => "false",
        };
        match self.message.is_empty() {
            true => said.to_string(),
            false => format!("{said}: {}", self.message),
        }
    }

    /// The condition as a CRD's status would list it: "Loaded False
    /// Invalid".
    pub fn title(&self) -> String {
        format!("{} {} {}", self.kind, self.status, self.reason)
    }
}

impl fmt::Display for Status {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str(match self {
            Self::True => "True",
            Self::False => "False",
        })
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::loaded(Condition::loaded(), false)]
    #[case::left_out(Condition::left_out("Invalid", "no spec"), true)]
    #[case::a_failed_search(Condition::search_failed("the store is unreachable"), true)]
    #[case::unmatched(Condition::unmatched("no Deployment web"), true)]
    #[case::ambiguous(Condition::ambiguous("2 Adapters hold aa:bb"), true)]
    #[case::not_served(Condition::not_served("no widgets"), true)]
    #[case::unattached(Condition::unattached("depends-on on a Service"), true)]
    #[case::an_unknown_layer(Condition::unknown_layer("no layer edge"), true)]
    #[case::a_conflict(Condition::conflict("why is set twice"), true)]
    fn says_which_conditions_to_act_on(#[case] condition: Condition, #[case] trouble: bool) {
        assert_eq!(condition.trouble(), trouble);
    }

    #[rstest]
    #[case::loaded(Condition::loaded(), "loaded")]
    #[case::left_out(Condition::left_out("Invalid", "no spec"), "not loaded: no spec")]
    #[case::unmatched(
        Condition::unmatched("no Deployment web"),
        "matches nothing: no Deployment web"
    )]
    #[case::a_conflict(Condition::conflict("why is set twice"), "set twice: why is set twice")]
    fn says_a_condition_in_words(#[case] condition: Condition, #[case] text: &str) {
        assert_eq!(condition.text(), text);
    }
}
