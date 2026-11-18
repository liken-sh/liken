// The environment variables that `media-operator` sets on a screen client's
// container. `media-operator/wire.go` declares the same names, so change
// both together. A pod cannot find the broker's address or its topics on
// its own, so the operator passes them down. An operator that draws the
// idle screen in place of `media-operator` reads the same values from the
// `Player`'s `status.idle` and sets the same variables.

use std::time::Duration;

/// The broker's address, as `host` or `host:port`. The port defaults to
/// 1883. An IPv6 address takes a port only in brackets, as in `[::1]:1883`.
pub const BUS_ADDRESS: &str = "MEDIA_BUS_ADDRESS";

/// The `Player`'s `metadata.name`, which `media-operator` publishes on a
/// controller's focus topic. A client with no name here matches no focus
/// message, so it ignores every press.
pub const PLAYER_NAME: &str = "MEDIA_PLAYER_NAME";

/// The `Player`'s retained status topic. See [`crate::status::Status`].
pub const STATUS_TOPIC: &str = "MEDIA_PLAYER_STATUS_TOPIC";

/// The `Player`'s retained volume topic, which only `media-operator`
/// writes. The operator sets this variable only when the `Player` lists
/// speakers, so when it is empty the client subscribes to no level and
/// draws no volume row.
pub const VOLUME_TOPIC: &str = "MEDIA_PLAYER_VOLUME_TOPIC";

/// The `host:port` that this client's `/metrics` listener binds. When it
/// is empty, no listener opens.
pub const METRICS_ADDRESS: &str = "MEDIA_METRICS_ADDRESS";

/// The tag of this client's image, for the `liken_build_info` gauge. The
/// operator leaves it unset when the image has no tag.
pub const VERSION: &str = "MEDIA_VERSION";

/// The `Player`'s commands topic. The playback pod publishes `play-next`
/// here when a person accepts the up-next offer on the scrubber. Key
/// presses arrive on each controller's events topic, not here.
pub const COMMANDS_TOPIC: &str = "MEDIA_PLAYER_COMMANDS_TOPIC";

/// The topic where the client publishes whether the panel should be on.
/// `media-operator` reads it and sets the screen's `Display`, because the
/// client has no API credentials.
pub const PANEL_TOPIC: &str = "MEDIA_PLAYER_PANEL_TOPIC";

/// The topic where `media-operator` publishes wake and sleep for the
/// screen, when the `Player`'s `spec.power` changes or a TV requests the
/// screen over HDMI-CEC.
pub const POWER_TOPIC: &str = "MEDIA_PLAYER_POWER_TOPIC";

/// The `Player`'s controllers, as two newline-separated lists in
/// `spec.remotes` order: each controller's events topic, and its focus
/// topic on the same line. A controller with no focus topic has a blank
/// line in the focus list, so each line number stays the controller's index
/// in `spec.remotes` and in [`crate::Moment::Focus`].
pub const REMOTE_EVENTS_TOPICS: &str = "MEDIA_REMOTE_EVENTS_TOPICS";
pub const REMOTE_FOCUS_TOPICS: &str = "MEDIA_REMOTE_FOCUS_TOPICS";

/// The seconds that an idle `Player` waits with no press before the screen
/// sleeps, and before the panel turns off. Zero, a missing value, or an
/// invalid value disables the timer.
pub const FADE_AFTER_SECONDS: &str = "IDLE_FADE_AFTER_SECONDS";
pub const OFF_AFTER_SECONDS: &str = "IDLE_OFF_AFTER_SECONDS";

/// One controller of the `Player`. When `focus` is empty, the client
/// ignores the controller's presses, because a press counts only while the
/// controller's focus topic names this `Player`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Remote {
    pub events: String,
    pub focus: String,
}

/// Everything the operator set in this container's environment.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Wiring {
    pub bus_address: String,
    pub player_name: String,
    pub status_topic: String,
    pub volume_topic: String,
    pub commands_topic: String,
    pub panel_topic: String,
    /// When empty, the client subscribes to no power topic.
    pub power_topic: String,
    /// In `spec.remotes` order.
    pub remotes: Vec<Remote>,
    /// When zero, the screen never sleeps.
    pub fade_after: Duration,
    /// Raised to at least `fade_after`, so the panel never turns off before
    /// the screen sleeps. When zero, the panel stays on.
    pub off_after: Duration,
    pub metrics_address: String,
    pub version: String,
}

impl Wiring {
    pub fn from_environment() -> Self {
        Self::read(|name| std::env::var(name).ok())
    }

    /// Tests pass their own source of variables, because every test in the
    /// binary shares the process environment.
    pub fn read(value: impl Fn(&str) -> Option<String>) -> Self {
        let read = |name| value(name).unwrap_or_default();

        let fade_after = seconds(&read(FADE_AFTER_SECONDS));
        let off_after = seconds(&read(OFF_AFTER_SECONDS));
        Self {
            bus_address: read(BUS_ADDRESS),
            player_name: read(PLAYER_NAME),
            status_topic: read(STATUS_TOPIC),
            volume_topic: read(VOLUME_TOPIC),
            commands_topic: read(COMMANDS_TOPIC),
            panel_topic: read(PANEL_TOPIC),
            power_topic: read(POWER_TOPIC),
            remotes: remotes(&read(REMOTE_EVENTS_TOPICS), &read(REMOTE_FOCUS_TOPICS)),
            fade_after,
            // Zero keeps the panel on, so it is not raised.
            off_after: match off_after.is_zero() {
                true => Duration::ZERO,
                false => off_after.max(fade_after),
            },
            metrics_address: read(METRICS_ADDRESS),
            version: read(VERSION),
        }
    }
}

/// Pairs each events topic with the focus topic on the same line. A focus
/// list that is shorter leaves the last controllers with no focus topic.
fn remotes(events: &str, focuses: &str) -> Vec<Remote> {
    let focuses = lines(focuses);
    lines(events)
        .into_iter()
        .enumerate()
        .map(|(index, events)| Remote {
            events,
            focus: focuses.get(index).cloned().unwrap_or_default(),
        })
        .collect()
}

/// An empty variable is an empty list, not a list of one blank line.
fn lines(text: &str) -> Vec<String> {
    if text.is_empty() {
        return Vec::new();
    }
    text.split('\n').map(str::to_string).collect()
}

/// Anything but a positive whole number of seconds is zero, so a bad value
/// disables the timer.
fn seconds(text: &str) -> Duration {
    match text.trim().parse::<i64>() {
        Ok(seconds) if seconds > 0 => Duration::from_secs(seconds as u64),
        _ => Duration::ZERO,
    }
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
    fn an_environment_with_none_of_the_variables_reads_as_the_default_wiring() {
        // The test binary runs with none of these variables set.
        assert_eq!(Wiring::from_environment(), Wiring::default());
    }

    #[test]
    fn a_source_with_no_values_reads_as_the_default_wiring() {
        assert_eq!(Wiring::read(|_| None), Wiring::default());
    }

    #[test]
    fn every_variable_is_read_into_its_field() {
        let read = wiring(&[
            (BUS_ADDRESS, "broker:1883"),
            (PLAYER_NAME, "player-1"),
            (STATUS_TOPIC, "media/players/default/player-1/status"),
            (VOLUME_TOPIC, "media/players/default/player-1/volume"),
            (COMMANDS_TOPIC, "media/players/default/player-1/commands"),
            (PANEL_TOPIC, "media/players/default/player-1/panel"),
            (POWER_TOPIC, "media/players/default/player-1/power"),
            (REMOTE_EVENTS_TOPICS, "events/remote-1\nevents/remote-2"),
            (REMOTE_FOCUS_TOPICS, "focus/remote-1\nfocus/remote-2"),
            (FADE_AFTER_SECONDS, "600"),
            (OFF_AFTER_SECONDS, "1800"),
            (METRICS_ADDRESS, "0.0.0.0:9200"),
            (VERSION, "2026.09.10-001"),
        ]);

        assert_eq!(read.bus_address, "broker:1883");
        assert_eq!(read.player_name, "player-1");
        assert_eq!(read.status_topic, "media/players/default/player-1/status");
        assert_eq!(read.volume_topic, "media/players/default/player-1/volume");
        assert_eq!(
            read.commands_topic,
            "media/players/default/player-1/commands"
        );
        assert_eq!(read.panel_topic, "media/players/default/player-1/panel");
        assert_eq!(read.power_topic, "media/players/default/player-1/power");
        assert_eq!(read.fade_after, Duration::from_secs(600));
        assert_eq!(read.off_after, Duration::from_secs(1800));
        assert_eq!(read.metrics_address, "0.0.0.0:9200");
        assert_eq!(read.version, "2026.09.10-001");
    }

    #[test]
    fn the_events_and_focus_lists_pair_by_line() {
        let read = wiring(&[
            (REMOTE_EVENTS_TOPICS, "events/remote-1\nevents/remote-2"),
            (REMOTE_FOCUS_TOPICS, "focus/remote-1\nfocus/remote-2"),
        ]);

        assert_eq!(
            read.remotes,
            [
                Remote {
                    events: "events/remote-1".into(),
                    focus: "focus/remote-1".into()
                },
                Remote {
                    events: "events/remote-2".into(),
                    focus: "focus/remote-2".into()
                },
            ]
        );
    }

    #[test]
    fn a_blank_focus_line_leaves_that_controller_with_no_focus_topic() {
        let read = wiring(&[
            (REMOTE_EVENTS_TOPICS, "events/remote-1\nevents/remote-2"),
            (REMOTE_FOCUS_TOPICS, "\nfocus/remote-2"),
        ]);

        assert_eq!(read.remotes[0].focus, "");
        assert_eq!(read.remotes[1].focus, "focus/remote-2");
    }

    #[test]
    fn a_focus_list_shorter_than_the_events_list_leaves_the_last_controllers_with_no_focus_topic() {
        let read = wiring(&[
            (REMOTE_EVENTS_TOPICS, "events/remote-1\nevents/remote-2"),
            (REMOTE_FOCUS_TOPICS, "focus/remote-1"),
        ]);

        assert_eq!(read.remotes[0].focus, "focus/remote-1");
        assert_eq!(read.remotes[1].focus, "");
    }

    #[test]
    fn an_empty_list_has_no_lines_and_a_trailing_newline_adds_a_blank_one() {
        assert!(lines("").is_empty());
        assert_eq!(lines("first"), ["first"]);
        assert_eq!(lines("first\n"), ["first", ""]);
        assert_eq!(lines("first\nsecond"), ["first", "second"]);
    }

    #[test]
    fn a_timer_is_read_as_whole_seconds_with_spaces_trimmed() {
        assert_eq!(seconds("600"), Duration::from_secs(600));
        assert_eq!(seconds(" 60 "), Duration::from_secs(60));
    }

    #[test]
    fn a_timer_that_is_not_a_positive_integer_is_zero() {
        assert_eq!(seconds("0"), Duration::ZERO);
        assert_eq!(seconds(""), Duration::ZERO);
        assert_eq!(seconds("soon"), Duration::ZERO);
        assert_eq!(seconds("-5"), Duration::ZERO);
    }

    #[test]
    fn an_off_timer_shorter_than_the_fade_timer_is_raised_to_it() {
        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "60")]);
        assert_eq!(read.off_after, Duration::from_secs(600));

        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "600")]);
        assert_eq!(read.off_after, Duration::from_secs(600));
    }

    #[test]
    fn an_off_timer_of_zero_or_garbage_stays_zero_whatever_the_fade_timer_is() {
        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "0")]);
        assert_eq!(read.off_after, Duration::ZERO);

        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "soon")]);
        assert_eq!(read.off_after, Duration::ZERO);
    }
}
