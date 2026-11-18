// The environment variables that only the idle screen reads.
// `wire.go` in `media-operator` declares the names that the operator
// sets, so a name here must match the name there. The `media-screen`
// crate reads the rest of the environment through
// `media_screen::Wiring`.

use std::time::Duration;

/// The Player's display name, and its components joined with newlines.
/// The screen draws them until the first status message replaces them,
/// so the first frame is not blank and a run on a workstation needs no
/// broker.
pub const PLAYER_NAME: &str = "IDLE_PLAYER_NAME";
pub const PLAYER_COMPONENTS: &str = "IDLE_PLAYER_COMPONENTS";

/// The seconds the client waits for a window before it exits. An unset
/// or non-positive value turns the watchdog off, so a run outside a pod
/// does not exit when it has no window.
pub const WINDOW_GRACE: &str = "IDLE_WINDOW_GRACE_SECONDS";

/// Set to `1` to bind the preview keys, which change the screen's state
/// from the keyboard in place of bus messages. `local/idle` sets it for
/// a run on a workstation. The operator never sets it, so an idle
/// screen in a cluster binds no preview keys and draws no key legend.
pub const PREVIEW: &str = "IDLE_PREVIEW";

/// The configuration this process reads from its environment.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Wiring {
    /// The variables that `media_screen::Wiring` reads.
    pub screen: media_screen::Wiring,
    pub player_name: String,
    pub components: Vec<String>,
    pub window_grace: Option<Duration>,
    pub preview: bool,
}

impl Wiring {
    /// Reads the configuration from this process's environment.
    pub fn from_environment() -> Self {
        Self::read(|name| std::env::var(name).ok())
    }

    /// Reads the configuration from `value` instead of the environment.
    /// The environment is shared by every test in the binary, so a test
    /// passes its variables here and does not set them.
    pub fn read(value: impl Fn(&str) -> Option<String>) -> Self {
        let read = |name| value(name).unwrap_or_default();

        Self {
            // `media_screen::Wiring` reads its own variables through the
            // same lookup, so this file does not list them.
            screen: media_screen::Wiring::read(&value),
            player_name: read(PLAYER_NAME),
            components: split_lines(&read(PLAYER_COMPONENTS)),
            window_grace: grace(&read(WINDOW_GRACE)),
            // Only the exact value `1` binds the keys, so a stray
            // `IDLE_PREVIEW` in a pod's environment cannot put the key
            // legend on a real screen.
            preview: read(PREVIEW) == "1",
        }
    }
}

/// Splits the newline-joined components and drops blank lines, so the
/// identity block draws no empty row. The operator leaves the variable
/// unset for a Player with no components, which gives an empty list.
pub fn split_lines(text: &str) -> Vec<String> {
    text.lines()
        .filter(|line| !line.is_empty())
        .map(str::to_string)
        .collect()
}

/// Parses the window grace in seconds. Anything but a positive, finite
/// number gives `None`, which turns the watchdog off.
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
    fn an_empty_environment_gives_the_default_wiring() {
        assert_eq!(Wiring::read(|_| None), Wiring::default());
    }

    #[test]
    fn a_process_reads_its_own_environment() {
        assert_eq!(Wiring::from_environment(), Wiring::default());
    }

    #[test]
    fn every_variable_this_client_reads_is_read_into_the_wiring() {
        let read = wiring(&[
            (PLAYER_NAME, "The Den"),
            (PLAYER_COMPONENTS, "The screen\nThe speakers"),
            (WINDOW_GRACE, "30"),
        ]);

        assert_eq!(read.player_name, "The Den");
        assert_eq!(read.components, ["The screen", "The speakers"]);
        assert_eq!(read.window_grace, Some(Duration::from_secs(30)));
    }

    #[test]
    fn the_media_screen_variables_are_read_into_its_wiring() {
        let read = wiring(&[
            (media_screen::wiring::BUS_ADDRESS, "broker:1883"),
            (
                media_screen::wiring::STATUS_TOPIC,
                "media/players/den/tv/status",
            ),
            (media_screen::wiring::FADE_AFTER_SECONDS, "600"),
        ]);

        assert_eq!(read.screen.bus_address, "broker:1883");
        assert_eq!(read.screen.status_topic, "media/players/den/tv/status");
        assert_eq!(read.screen.fade_after, Duration::from_secs(600));
    }

    #[test]
    fn an_empty_value_has_no_components() {
        assert!(
            split_lines("").is_empty(),
            "split_lines of an empty string returns lines, want none"
        );
        assert_eq!(split_lines("The screen"), ["The screen"]);
    }

    #[test]
    fn a_blank_line_is_dropped_from_the_components() {
        assert_eq!(
            split_lines("The screen\n\nA remote\n"),
            ["The screen", "A remote"]
        );
    }

    #[test]
    fn a_positive_grace_is_parsed() {
        assert_eq!(grace("30"), Some(Duration::from_secs(30)));
        assert_eq!(grace(" 1.5 "), Some(Duration::from_secs_f64(1.5)));
    }

    #[test]
    fn anything_but_a_positive_grace_gives_none() {
        assert_eq!(grace(""), None);
        assert_eq!(grace("0"), None);
        assert_eq!(grace("-5"), None);
        assert_eq!(grace("soon"), None);
        assert_eq!(grace("inf"), None);
    }

    #[test]
    fn only_the_value_1_binds_the_preview_keys() {
        assert!(
            wiring(&[(PREVIEW, "1")]).preview,
            "preview is off when the variable is 1"
        );
        assert!(
            !wiring(&[(PREVIEW, "true")]).preview,
            "preview is on when the variable is true, want only 1 to turn it on"
        );
        assert!(
            !wiring(&[]).preview,
            "preview is on when the variable is not set"
        );
    }
}
