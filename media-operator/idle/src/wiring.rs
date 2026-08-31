// The variables the operator sets on the idle container, and what this client
// reads from each one. `media-operator` names them in `wire.go` on the writing
// side, so the two files are one contract and a name here matches a name
// there.
//
// A pod cannot discover the broker in front of it, the topic base its cluster
// chose, or the Wayland app-id its display claim delivered. The operator holds
// all three and passes them down, so every value below arrives in the
// environment and none is guessed.

use std::time::Duration;

/// The broker's address, written `host:port`.
pub const BUS_ADDRESS: &str = "MEDIA_BUS_ADDRESS";

/// The `Player`'s retained status topic. It carries the display name, the
/// activity, the current `Play`, and the parts.
pub const STATUS_TOPIC: &str = "MEDIA_PLAYER_STATUS_TOPIC";

/// The `Player`'s retained volume topic. The operator sets it only for a unit
/// that states sinks, so an empty value is the speaker gate: the client
/// subscribes to no level and draws no volume row.
pub const VOLUME_TOPIC: &str = "MEDIA_PLAYER_VOLUME_TOPIC";

/// The `Player`'s screen topic, which carries the sidecar's own decisions.
/// Nothing on it is retained.
pub const SCREEN_TOPIC: &str = "MEDIA_PLAYER_SCREEN_TOPIC";

/// The unit's friendly name, and its parts joined with newlines. They seed the
/// identity block, so the first frame is never blank and a workstation needs no
/// broker. The first status replaces both.
pub const PLAYER_NAME: &str = "IDLE_PLAYER_NAME";
pub const PLAYER_COMPONENTS: &str = "IDLE_PLAYER_COMPONENTS";

/// The Wayland app-id the surface must request. The display operator writes it
/// into the container at run time from the claim's CDI spec, and the
/// compositor routes the surface to the right screen by it.
pub const APP_ID: &str = "DISPLAY_APP_ID";

/// The seconds the client waits for a window before it exits. An unset or
/// non-positive value leaves the watchdog off, so a run outside a pod never
/// exits for a missing window.
pub const WINDOW_GRACE: &str = "IDLE_WINDOW_GRACE_SECONDS";

/// The variable that binds the preview keys, which stand in for the bus on a
/// workstation. `local/idle` sets it, and the operator sets it on no pod, so a
/// cluster's idle screen binds no key and draws no legend.
pub const PREVIEW: &str = "IDLE_PREVIEW";

/// The three topics the client subscribes to. An empty topic is one the
/// operator did not set.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Topics {
    pub status: String,
    pub volume: String,
    pub screen: String,
}

/// Everything the operator told this container.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Wiring {
    pub bus_address: String,
    pub topics: Topics,
    pub player_name: String,
    pub components: Vec<String>,
    pub app_id: String,
    pub window_grace: Option<Duration>,
    /// Whether the preview keys stand in for the bus. It is true only where
    /// [`PREVIEW`] is `1`.
    pub preview: bool,
}

impl Wiring {
    /// What this process runs under.
    pub fn from_environment() -> Self {
        Self::read(|name| std::env::var(name).ok())
    }

    /// The same read against any source of values. The environment is global to
    /// a process, so a test states the variables here instead of setting them
    /// and racing every other test in the binary.
    pub fn read(value: impl Fn(&str) -> Option<String>) -> Self {
        let read = |name| value(name).unwrap_or_default();

        Self {
            bus_address: read(BUS_ADDRESS),
            topics: Topics {
                status: read(STATUS_TOPIC),
                volume: read(VOLUME_TOPIC),
                screen: read(SCREEN_TOPIC),
            },
            player_name: read(PLAYER_NAME),
            components: split_lines(&read(PLAYER_COMPONENTS)),
            app_id: read(APP_ID),
            window_grace: grace(&read(WINDOW_GRACE)),
            // One exact value binds the keys. Anything else, an unset variable
            // included, leaves them unbound, so a variable that survives into
            // a pod by accident cannot put a legend on a screen in a house.
            preview: read(PREVIEW) == "1",
        }
    }
}

/// The parts, one per line. The operator joins them with newlines and sets the
/// variable only for a unit that lists any, so an absent value is a unit with
/// no parts and not an error. A blank line names no part and draws nothing, so
/// it is dropped here rather than left as an empty row in the identity block.
pub fn split_lines(text: &str) -> Vec<String> {
    text.lines()
        .filter(|line| !line.is_empty())
        .map(str::to_string)
        .collect()
}

/// The window grace, in seconds. Anything but a positive number leaves the
/// watchdog off, which is the rule `display/window.lua` applies to the same
/// variable.
fn grace(text: &str) -> Option<Duration> {
    let seconds: f64 = text.trim().parse().ok()?;
    if seconds <= 0.0 || !seconds.is_finite() {
        return None;
    }
    Some(Duration::from_secs_f64(seconds))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn wiring(pairs: &[(&str, &str)]) -> Wiring {
        let pairs: Vec<(String, String)> = pairs
            .iter()
            .map(|(name, value)| (name.to_string(), value.to_string()))
            .collect();
        Wiring::read(|name| {
            pairs
                .iter()
                .find(|(set, _)| set == name)
                .map(|(_, value)| value.clone())
        })
    }

    #[test]
    fn an_empty_environment_wires_nothing() {
        assert_eq!(Wiring::read(|_| None), Wiring::default());
    }

    #[test]
    fn every_variable_lands_in_the_wiring() {
        let read = wiring(&[
            (BUS_ADDRESS, "broker:1883"),
            (STATUS_TOPIC, "media/players/den/tv/status"),
            (VOLUME_TOPIC, "media/players/den/tv/volume"),
            (SCREEN_TOPIC, "media/players/den/tv/screen"),
            (PLAYER_NAME, "The Den"),
            (PLAYER_COMPONENTS, "The screen\nThe speakers"),
            (APP_ID, "media-den-tv"),
            (WINDOW_GRACE, "30"),
        ]);

        assert_eq!(read.bus_address, "broker:1883");
        assert_eq!(read.topics.status, "media/players/den/tv/status");
        assert_eq!(read.topics.volume, "media/players/den/tv/volume");
        assert_eq!(read.topics.screen, "media/players/den/tv/screen");
        assert_eq!(read.player_name, "The Den");
        assert_eq!(read.components, ["The screen", "The speakers"]);
        assert_eq!(read.app_id, "media-den-tv");
        assert_eq!(read.window_grace, Some(Duration::from_secs(30)));
    }

    #[test]
    fn a_unit_with_no_parts_lists_none() {
        assert!(split_lines("").is_empty());
        assert_eq!(split_lines("The screen"), ["The screen"]);
    }

    #[test]
    fn a_blank_line_names_no_part() {
        assert_eq!(
            split_lines("The screen\n\nA remote\n"),
            ["The screen", "A remote"]
        );
    }

    #[test]
    fn a_positive_grace_arms_the_watchdog() {
        assert_eq!(grace("30"), Some(Duration::from_secs(30)));
        assert_eq!(grace(" 1.5 "), Some(Duration::from_secs_f64(1.5)));
    }

    #[test]
    fn anything_but_a_positive_grace_leaves_it_off() {
        assert_eq!(grace(""), None);
        assert_eq!(grace("0"), None);
        assert_eq!(grace("-5"), None);
        assert_eq!(grace("soon"), None);
        assert_eq!(grace("inf"), None);
    }
}
