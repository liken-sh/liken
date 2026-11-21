//! The conditions that the `/resources` page shows for each document
//! `transect` reads. They have the fields of a Kubernetes condition, so
//! that `transect` could write them to `status.conditions` if its
//! resources were served as CRDs.

use std::fmt;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status {
    True,
    False,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Condition {
    /// The condition's type, for example `Loaded`.
    pub kind: &'static str,
    pub status: Status,
    /// A CamelCase word that a program can match on, for example
    /// `Invalid`.
    pub reason: &'static str,
    pub message: String,
}

impl Condition {
    /// The document was read and `transect` uses it.
    pub fn loaded() -> Self {
        Self {
            kind: "Loaded",
            status: Status::True,
            reason: "Loaded",
            message: String::new(),
        }
    }

    /// `transect` ignored the document because of `reason`.
    pub fn left_out(reason: &'static str, message: impl Into<String>) -> Self {
        Self {
            kind: "Loaded",
            status: Status::False,
            reason,
            message: message.into(),
        }
    }

    /// The document refers to something that is not in the cluster, for
    /// example a selector that matches nothing.
    pub fn unmatched(message: impl Into<String>) -> Self {
        Self {
            kind: "Matched",
            status: Status::False,
            reason: "NoMatch",
            message: message.into(),
        }
    }

    /// A reference by value matched more than one object, so
    /// `transect` draws no edge to any of them.
    pub fn ambiguous(message: impl Into<String>) -> Self {
        Self {
            kind: "Matched",
            status: Status::False,
            reason: "Ambiguous",
            message: message.into(),
        }
    }

    /// The document refers to a kind that the cluster does not serve.
    pub fn not_served(message: impl Into<String>) -> Self {
        Self {
            kind: "Served",
            status: Status::False,
            reason: "NotServed",
            message: message.into(),
        }
    }

    /// The document sets a field on a kind that `transect` does not read
    /// the field from, for example dependencies on a `Service`.
    pub fn unattached(message: impl Into<String>) -> Self {
        Self {
            kind: "Attached",
            status: Status::False,
            reason: "DrawsNothing",
            message: message.into(),
        }
    }

    /// The document refers to a layer that the cluster's `Layers` does
    /// not declare, so `transect` ignores that part of the document.
    pub fn unknown_layer(message: impl Into<String>) -> Self {
        Self {
            kind: "LayerDeclared",
            status: Status::False,
            reason: "UnknownLayer",
            message: message.into(),
        }
    }

    /// Two documents set the same value. The message says which
    /// document `transect` uses.
    pub fn conflict(message: impl Into<String>) -> Self {
        Self {
            kind: "Conflict",
            status: Status::True,
            reason: "SetTwice",
            message: message.into(),
        }
    }

    /// A role of an elevation names a shape that `transect` has not
    /// loaded, so the role is drawn in the default shape.
    pub fn unknown_shape(message: impl Into<String>) -> Self {
        Self {
            kind: "ShapesFound",
            status: Status::False,
            reason: "UnknownShape",
            message: message.into(),
        }
    }

    /// A wire of an elevation meets a role whose shape has no port on
    /// the facing edge, so the page does not draw the wire.
    pub fn no_port(message: impl Into<String>) -> Self {
        Self {
            kind: "ShapesFound",
            status: Status::False,
            reason: "NoPort",
            message: message.into(),
        }
    }

    /// A `through` names a reference that the knowledge of another kind
    /// does not confirm, so the page shows this message in place of what
    /// the reference leads to.
    pub fn unresolved(message: impl Into<String>) -> Self {
        Self {
            kind: "Resolved",
            status: Status::False,
            reason: "Unresolved",
            message: message.into(),
        }
    }

    /// The log store rejected the resource's last search, or the search
    /// failed.
    pub fn search_failed(message: impl Into<String>) -> Self {
        Self {
            kind: "Searched",
            status: Status::False,
            reason: "SearchFailed",
            message: message.into(),
        }
    }

    /// Whether the condition reports a problem for a person to fix.
    /// `Conflict` is a problem when it is `True`, and every other type
    /// when it is `False`.
    pub fn trouble(&self) -> bool {
        match (self.kind, self.reason) {
            (
                "Loaded" | "Matched" | "Served" | "LayerDeclared" | "Attached" | "Searched"
                | "ShapesFound" | "Resolved",
                _,
            ) => self.status == Status::False,
            _ => self.status == Status::True,
        }
    }

    /// The condition in words, followed by its message, as in "loaded"
    /// or "not loaded: no spec". `title` returns the type, status, and
    /// reason for a reader that matches on them.
    pub fn text(&self) -> String {
        let said = match (self.kind, self.status) {
            ("Loaded", Status::True) => "loaded",
            ("Loaded", Status::False) => "not loaded",
            ("Matched", Status::False) if self.reason == "Ambiguous" => "matches more than one",
            ("Matched", Status::False) => "matches nothing",
            ("Served", Status::False) => "refers to a kind the cluster does not serve",
            ("Attached", Status::False) => "draws nothing",
            ("LayerDeclared", Status::False) => "refers to a layer that Layers does not declare",
            ("Conflict", Status::True) => "set twice",
            ("Searched", Status::False) => "search failed",
            ("ShapesFound", Status::False) if self.reason == "NoPort" => {
                "has a wire to a shape with no port there"
            }
            ("ShapesFound", Status::False) => "names a shape transect has not loaded",
            ("Resolved", Status::False) => "follows a reference that does not resolve",
            (_, Status::True) => "true",
            (_, Status::False) => "false",
        };
        match self.message.is_empty() {
            true => said.to_string(),
            false => format!("{said}: {}", self.message),
        }
    }

    /// The type, status, and reason, in the order a CRD's status lists
    /// them, as in "Loaded False Invalid".
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
    #[case::an_unknown_shape(Condition::unknown_shape("the role amp names kettle"), true)]
    #[case::no_port(Condition::no_port("the wire meets a tag"), true)]
    #[case::unresolved(Condition::unresolved("Pad has no knowledge"), true)]
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
    #[case::no_port(
        Condition::no_port("the wire meets a tag"),
        "has a wire to a shape with no port there: the wire meets a tag"
    )]
    #[case::unresolved(
        Condition::unresolved("Pad has no knowledge"),
        "follows a reference that does not resolve: Pad has no knowledge"
    )]
    fn says_a_condition_in_words(#[case] condition: Condition, #[case] text: &str) {
        assert_eq!(condition.text(), text);
    }
}
