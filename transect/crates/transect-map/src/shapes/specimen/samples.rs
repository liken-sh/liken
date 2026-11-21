//! The texts that `/shapes` draws on every shape, one for each state of
//! plan 17's table and for each pose: readings, a long name, what the
//! page's own object shows, a stack, and a turned part. The names are
//! invented, and no sample is of one kind of system, so a reader judges
//! the shape and not the sample.

use crate::shapes::text::{End, Nothing, Part, Reading, Shown, State, Why};
use transect_graph::links::Reference;

/// One cell's text, state, and pose.
pub struct Sample {
    pub caption: &'static str,
    pub label: &'static str,
    pub source: &'static str,
    pub state: State,
    /// The name the part links to, or what an empty role shows.
    pub end: &'static str,
    pub under: Option<&'static str>,
    pub why: Option<&'static str>,
    pub readings: &'static [(&'static str, &'static str)],
    pub shows: &'static [(&'static str, &'static str)],
    pub stack: u8,
    /// Whether the cell draws the shape on its side. Only a shape that
    /// turns has the cell.
    pub turned: bool,
}

const PLAIN: Sample = Sample {
    caption: "plain, with readings, and bars that mark its ports",
    label: "powered by",
    source: "from spec.supply",
    state: State::Plain,
    end: "Supply bench-supply",
    under: None,
    why: None,
    readings: &[
        ("model", "PS-30"),
        ("power", "On"),
        ("output", "12 V"),
        ("load", "42%"),
    ],
    shows: &[],
    stack: 0,
    turned: false,
};

pub const CELLS: &[Sample] = &[
    PLAIN,
    Sample {
        caption: "plain, a long name and no readings",
        label: "runs on",
        source: "from status.node",
        end: "Node a-machine-with-a-long-name-in-rack-0001",
        under: Some("reaches machine a-machine-with-a-long-name-in-rack-0001"),
        readings: &[],
        ..PLAIN
    },
    Sample {
        caption: "plain, with what the page's own object shows",
        label: "shows on",
        source: "from status.panel",
        end: "Panel lobby-panel",
        readings: &[("connector", "HDMI-A-1"), ("mode", "1920x1080@60")],
        shows: &[
            ("Activity", "Showing"),
            (
                "Title",
                "A Tale of Two Cities, in an edition whose title is long",
            ),
        ],
        ..PLAIN
    },
    Sample {
        caption: "a warning",
        state: State::Warning,
        why: Some("Degraded"),
        readings: &[("model", "PS-30"), ("power", "On")],
        ..PLAIN
    },
    Sample {
        caption: "a name with no object, drawn as a warning",
        state: State::NoObject,
        under: Some("the cluster has no such Supply"),
        readings: &[],
        ..PLAIN
    },
    Sample {
        caption: "failing",
        state: State::Failing,
        why: Some("Unreachable"),
        readings: &[("model", "PS-30"), ("power", "Off")],
        ..PLAIN
    },
    Sample {
        caption: "at rest",
        label: "cooled by",
        source: "from spec.fan",
        state: State::Resting,
        end: "Fan rack-fan",
        why: Some("Idle"),
        readings: &[("speed", "0 rpm")],
        ..PLAIN
    },
    Sample {
        caption: "a value, not an object",
        label: "configured from",
        source: "from spec.settings",
        state: State::Value,
        end: "settings.example.test/lobby",
        under: Some("not an object in the cluster"),
        readings: &[],
        ..PLAIN
    },
    Sample {
        caption: "an expected role with no part",
        state: State::Empty,
        end: "no Supply",
        under: Some("spec.supply names nothing now"),
        readings: &[],
        ..PLAIN
    },
    Sample {
        caption: "a role expected in another state",
        label: "served by",
        source: "from status.service",
        state: State::Elsewhere,
        end: "no Service",
        under: Some("status.service names nothing now"),
        readings: &[],
        ..PLAIN
    },
    Sample {
        caption: "a stack of alike parts",
        label: "replicas",
        source: "from status.replicas[]",
        end: "Pod orders-1",
        under: Some("ready 2 of 2"),
        readings: &[],
        stack: 2,
        ..PLAIN
    },
    Sample {
        caption: "on its side, in a wide area",
        label: "front",
        source: "from spec.front",
        state: State::Value,
        end: "center",
        under: None,
        readings: &[],
        turned: true,
        ..PLAIN
    },
];

impl Sample {
    /// The parts of the cell's role, or what its empty role shows.
    pub fn content(&self) -> (Vec<Part>, Option<Nothing>) {
        if matches!(self.state, State::Empty | State::Elsewhere) {
            let nothing = Nothing {
                text: self.end.into(),
                note: self.under.unwrap_or_default().into(),
                state: self.state,
            };
            return (Vec::new(), Some(nothing));
        }
        let part = Part {
            end: End {
                reference: Reference {
                    text: self.end.into(),
                    href: None,
                },
                under: self.under.map(Into::into),
                reaches: Vec::new(),
            },
            readings: self
                .readings
                .iter()
                .map(|(name, value)| Reading {
                    name: name.to_string(),
                    value: value.to_string(),
                    from: None,
                })
                .collect(),
            shows: self
                .shows
                .iter()
                .map(|(name, value)| Shown {
                    reading: Reading {
                        name: name.to_string(),
                        value: value.to_string(),
                        from: None,
                    },
                    fallback: false,
                })
                .collect(),
            state: self.state,
            literal: self.state == State::Value,
            why: self.why.map(|reason| Why {
                reason: reason.into(),
                message: None,
            }),
        };
        (vec![part], None)
    }
}
