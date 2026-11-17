//! The gates and plain boxes a workload's drawing is built of.

use transect_access::access::drawing::Gate;
use transect_access::access::ways::{self, State};
use transect_map::map::section_drawing::{Look as BoxLook, SectionBox};

/// A ways-in gate as a dot on a line.
pub fn gate(gate: &ways::Gate) -> Gate {
    let class = match gate.state {
        State::On => "ax-gate ax-gate-on",
        State::Half => "ax-gate ax-gate-half",
        State::Declared => "ax-gate ax-gate-declared",
        State::Off => "ax-gate ax-gate-off",
    };
    Gate {
        class,
        title: gate.title.clone(),
        declared: gate.state == State::Declared,
    }
}

pub fn on(title: String) -> Gate {
    Gate {
        class: "ax-gate ax-gate-on",
        title,
        declared: false,
    }
}

pub fn off(title: &str) -> Gate {
    Gate {
        class: "ax-gate ax-gate-off",
        title: title.into(),
        declared: false,
    }
}

pub fn plain(title: String, lines: Vec<String>, href: Option<String>) -> SectionBox {
    SectionBox::new(title, lines, BoxLook::Plain).with_href(href)
}
