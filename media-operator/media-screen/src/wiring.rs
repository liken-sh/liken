// The variables the operator sets on a screen client's container, and what
// this crate reads from each one. `media-operator` names them in `wire.go` on
// the writing side, so the two files are one contract and a name here matches
// a name there.
//
// The operator passes all of this down, because a pod cannot read the
// address of the broker in front of it or the topic base a cluster
// chose. A delegate's operator reads the same facts off `status.idle`
// on the `Player` and sets the same variables, so one contract serves
// the client this project ships and any other.

use std::time::Duration;

/// The broker's address, written `host:port`.
pub const BUS_ADDRESS: &str = "MEDIA_BUS_ADDRESS";

/// The `Player`'s own object name, the value every focus mark holds. It is
/// not the friendly name `IDLE_PLAYER_NAME` carries, because the operator
/// writes marks from `metadata.name`. A client that reads no name matches no
/// mark and answers no press.
pub const PLAYER_NAME: &str = "MEDIA_PLAYER_NAME";

/// The `Player`'s retained status topic. It carries the display name, the
/// activity, the current `Play`, and the parts.
pub const STATUS_TOPIC: &str = "MEDIA_PLAYER_STATUS_TOPIC";

/// The `Player`'s retained volume topic. The operator sets it only for a unit
/// that states sinks, so an empty value is the speaker gate: the client
/// subscribes to no level, draws no volume row, and answers no volume press.
pub const VOLUME_TOPIC: &str = "MEDIA_PLAYER_VOLUME_TOPIC";

/// The `Player`'s commands topic. It carries the operator's
/// `re-present` and nothing else: the presses reach a client on the
/// controllers' own topics, and a client brings its own shade down in
/// its own process.
pub const COMMANDS_TOPIC: &str = "MEDIA_PLAYER_COMMANDS_TOPIC";

/// The topic the client states the panel desire on. The operator builds it
/// whole, the way it builds the commands and status topics, so the client
/// parses no topic.
pub const PANEL_TOPIC: &str = "MEDIA_PLAYER_PANEL_TOPIC";

/// The two lists of the unit's controllers, newline-joined and aligned by
/// position: each controller's events topic and the focus topic that carries
/// its mark. They are the same two variables the playback pod's command
/// sidecar reads. A line's number is the controller's place in `spec.remotes`,
/// which is the index a focus moment carries, so a blank focus line leaves
/// that controller with no focus topic rather than shifting the pairing.
pub const REMOTE_EVENTS_TOPICS: &str = "MEDIA_REMOTE_EVENTS_TOPICS";
pub const REMOTE_FOCUS_TOPICS: &str = "MEDIA_REMOTE_FOCUS_TOPICS";

/// The quiet window in seconds, where zero means the screen never fades on
/// its own, and the off window in seconds, where zero leaves the panel lit.
/// The operator settles both for every `Player`, so an unset or unreadable
/// value fades nothing and darkens nothing rather than guessing a window a
/// cluster never asked for.
pub const FADE_AFTER_SECONDS: &str = "IDLE_FADE_AFTER_SECONDS";
pub const OFF_AFTER_SECONDS: &str = "IDLE_OFF_AFTER_SECONDS";

/// One of the unit's controllers as a client reads it: the topic its presses
/// arrive on, and the topic its focus mark stands on. A controller with no
/// focus topic carries an empty one, and a press from it reaches nothing,
/// because the mark is the whole gate.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Remote {
    pub events: String,
    pub focus: String,
}

/// Everything the operator told this container.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Wiring {
    pub bus_address: String,
    pub player_name: String,
    pub status_topic: String,
    pub volume_topic: String,
    pub commands_topic: String,
    pub panel_topic: String,
    /// The controllers in `spec.remotes` order.
    pub remotes: Vec<Remote>,
    /// The quiet window. Zero never arms the timer.
    pub fade_after: Duration,
    /// The off window, clamped to at least the fade, so the panel never goes
    /// dark behind a still-lit image. Zero leaves the desire at on forever.
    pub off_after: Duration,
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

        let fade_after = seconds(&read(FADE_AFTER_SECONDS));
        let off_after = seconds(&read(OFF_AFTER_SECONDS));
        Self {
            bus_address: read(BUS_ADDRESS),
            player_name: read(PLAYER_NAME),
            status_topic: read(STATUS_TOPIC),
            volume_topic: read(VOLUME_TOPIC),
            commands_topic: read(COMMANDS_TOPIC),
            panel_topic: read(PANEL_TOPIC),
            remotes: remotes(&read(REMOTE_EVENTS_TOPICS), &read(REMOTE_FOCUS_TOPICS)),
            fade_after,
            // A window of zero is the panel never going dark, whatever the
            // fade is, so the clamp runs only on a window a cluster stated.
            off_after: match off_after.is_zero() {
                true => Duration::ZERO,
                false => off_after.max(fade_after),
            },
        }
    }
}

/// Pair each controller's events topic with the focus topic on the same line
/// of the second list. A blank line is kept, because the two lists stay
/// aligned by position and the line number is the controller's index.
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

/// One of the newline-joined lists the operator sets on a container. An unset
/// variable is no lines at all, not one empty line.
fn lines(text: &str) -> Vec<String> {
    if text.is_empty() {
        return Vec::new();
    }
    text.split('\n').map(str::to_string).collect()
}

/// One window, in seconds. Anything but a positive whole number is no window
/// at all, because the operator settles this field for every `Player` and a
/// guessed default here would dim a screen the cluster never asked to dim.
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
    fn a_process_reads_its_own_environment() {
        // The gates run this binary with none of these variables set, so what
        // the read returns is the empty wiring. A client under that wiring
        // opens no reader and draws its seeds.
        assert_eq!(Wiring::from_environment(), Wiring::default());
    }

    #[test]
    fn an_empty_environment_wires_nothing() {
        assert_eq!(Wiring::read(|_| None), Wiring::default());
    }

    #[test]
    fn every_variable_lands_in_the_wiring() {
        let read = wiring(&[
            (BUS_ADDRESS, "broker:1883"),
            (PLAYER_NAME, "den-tv"),
            (STATUS_TOPIC, "media/players/den/tv/status"),
            (VOLUME_TOPIC, "media/players/den/tv/volume"),
            (COMMANDS_TOPIC, "media/players/den/tv/commands"),
            (PANEL_TOPIC, "media/players/den/tv/panel"),
            (REMOTE_EVENTS_TOPICS, "events/sofa\nevents/armchair"),
            (REMOTE_FOCUS_TOPICS, "focus/sofa\nfocus/armchair"),
            (FADE_AFTER_SECONDS, "600"),
            (OFF_AFTER_SECONDS, "1800"),
        ]);

        assert_eq!(read.bus_address, "broker:1883");
        assert_eq!(read.player_name, "den-tv");
        assert_eq!(read.status_topic, "media/players/den/tv/status");
        assert_eq!(read.volume_topic, "media/players/den/tv/volume");
        assert_eq!(read.commands_topic, "media/players/den/tv/commands");
        assert_eq!(read.panel_topic, "media/players/den/tv/panel");
        assert_eq!(read.fade_after, Duration::from_secs(600));
        assert_eq!(read.off_after, Duration::from_secs(1800));
    }

    #[test]
    fn the_two_remote_lists_pair_by_position() {
        let read = wiring(&[
            (REMOTE_EVENTS_TOPICS, "events/sofa\nevents/armchair"),
            (REMOTE_FOCUS_TOPICS, "focus/sofa\nfocus/armchair"),
        ]);

        assert_eq!(
            read.remotes,
            [
                Remote {
                    events: "events/sofa".into(),
                    focus: "focus/sofa".into()
                },
                Remote {
                    events: "events/armchair".into(),
                    focus: "focus/armchair".into()
                },
            ]
        );
    }

    #[test]
    fn a_missing_focus_topic_leaves_that_controller_blank_and_shifts_nothing() {
        let read = wiring(&[
            (REMOTE_EVENTS_TOPICS, "events/sofa\nevents/armchair"),
            (REMOTE_FOCUS_TOPICS, "\nfocus/armchair"),
        ]);

        assert_eq!(read.remotes[0].focus, "");
        assert_eq!(read.remotes[1].focus, "focus/armchair");
    }

    #[test]
    fn a_focus_list_shorter_than_the_events_list_shifts_nothing() {
        let read = wiring(&[
            (REMOTE_EVENTS_TOPICS, "events/sofa\nevents/armchair"),
            (REMOTE_FOCUS_TOPICS, "focus/sofa"),
        ]);

        assert_eq!(read.remotes[0].focus, "focus/sofa");
        assert_eq!(read.remotes[1].focus, "");
    }

    #[test]
    fn a_unit_with_no_controllers_lists_none() {
        assert!(lines("").is_empty());
        assert_eq!(lines("first"), ["first"]);
        assert_eq!(lines("first\n"), ["first", ""]);
        assert_eq!(lines("first\nsecond"), ["first", "second"]);
    }

    #[test]
    fn the_quiet_window_reads_the_seconds() {
        assert_eq!(seconds("600"), Duration::from_secs(600));
        assert_eq!(seconds(" 60 "), Duration::from_secs(60));
    }

    #[test]
    fn anything_but_a_positive_window_is_no_window() {
        assert_eq!(seconds("0"), Duration::ZERO);
        assert_eq!(seconds(""), Duration::ZERO);
        assert_eq!(seconds("soon"), Duration::ZERO);
        assert_eq!(seconds("-5"), Duration::ZERO);
    }

    #[test]
    fn the_off_window_never_lands_before_the_fade() {
        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "60")]);
        assert_eq!(read.off_after, Duration::from_secs(600));

        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "600")]);
        assert_eq!(read.off_after, Duration::from_secs(600));
    }

    #[test]
    fn an_off_window_of_zero_leaves_the_panel_lit_whatever_the_fade_is() {
        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "0")]);
        assert_eq!(read.off_after, Duration::ZERO);

        let read = wiring(&[(FADE_AFTER_SECONDS, "600"), (OFF_AFTER_SECONDS, "soon")]);
        assert_eq!(read.off_after, Duration::ZERO);
    }
}
