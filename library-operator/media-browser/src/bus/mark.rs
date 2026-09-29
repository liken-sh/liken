// The mark a person sets on one title for everyone at the screen:
// watched, or cleared. The browser writes it and never reads it back. The
// progress role records it as one row of the progress store, and the
// jellyfin role sends it on to Jellyfin. `library-operator`'s
// `progressbus.go` holds the same shape as `titleMark`.
//
// Each mark is one topic, named after the `Player` and the second of the
// press, and it is retained. The person pressed once and nothing repeats
// the press, so a progress role that is down when the person presses must
// read the mark when it subscribes again. The progress role clears the
// topic once the retention has run.

use serde_json::{Map, Value};

use crate::catalog::Identity;

/// Which of the two marks a person set. Each is a position: watched is the
/// end of the work, and cleared is 0, which the thread rule reads as not
/// started.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TitleMark {
    Watched,
    Cleared,
}

impl TitleMark {
    /// The word the message carries in `mark`, which the log lines use too.
    pub fn word(self) -> &'static str {
        match self {
            Self::Watched => "watched",
            Self::Cleared => "cleared",
        }
    }

    /// The position the mark writes for a work of this duration.
    pub fn position(self, duration: i64) -> i64 {
        match self {
            Self::Watched => duration,
            Self::Cleared => 0,
        }
    }
}

/// The topic of one mark: `{branch}/mark-{player}-{at}/mark`, under the
/// branch of the tree the namespace's plays are in. The name is the row's
/// key in the progress store, so it is unique for each press on one
/// screen, as long as no two presses share a second. The browser moves a
/// second press within the same second to the next second.
pub fn topic(branch: &str, player: &str, at: i64) -> String {
    format!("{branch}/mark-{player}-{at}/mark")
}

/// The mark as bytes. `people` are the people at the screen, and
/// `identity` names the work the way a play request names it, so the
/// row the mark writes joins to the same catalog work as a play of it.
/// `at` is the second of the press, which the store writes as the row's
/// recorded time.
pub fn payload(
    mark: TitleMark,
    player: &str,
    people: &[String],
    identity: &Identity,
    duration: i64,
    at: i64,
) -> Vec<u8> {
    let mut message = Map::new();
    message.insert("mark".into(), Value::from(mark.word()));
    message.insert("player".into(), Value::from(player));
    message.insert(
        "people".into(),
        Value::Array(
            people
                .iter()
                .map(|name| Value::from(name.as_str()))
                .collect(),
        ),
    );
    message.insert(
        "aliases".into(),
        Value::Object(
            identity
                .aliases
                .iter()
                .map(|(provider, id)| (provider.clone(), Value::from(id.as_str())))
                .collect(),
        ),
    );
    message.insert("season".into(), Value::from(identity.season));
    message.insert("episode".into(), Value::from(identity.episode));
    message.insert("position".into(), Value::from(mark.position(duration)));
    message.insert("duration".into(), Value::from(duration));
    message.insert("at".into(), Value::from(at));
    Value::Object(message).to_string().into_bytes()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn episode() -> Identity {
        Identity {
            aliases: [("tvdb".to_string(), "8001".to_string())].into(),
            season: 2,
            episode: 5,
        }
    }

    #[test]
    fn a_mark_is_one_topic_under_the_plays_branch() {
        assert_eq!(
            topic("liken/library/plays/house", "den", 1_759_140_000),
            "liken/library/plays/house/mark-den-1759140000/mark"
        );
    }

    #[test]
    fn a_watched_mark_on_an_episode_carries_every_field_at_the_end_of_the_work() {
        let people = ["person-a".to_string(), "person-b".to_string()];
        let message: Value = serde_json::from_slice(&payload(
            TitleMark::Watched,
            "den",
            &people,
            &episode(),
            2_760,
            1_759_140_000,
        ))
        .expect("the mark is JSON");

        assert_eq!(
            message,
            serde_json::json!({
                "mark": "watched",
                "player": "den",
                "people": ["person-a", "person-b"],
                "aliases": {"tvdb": "8001"},
                "season": 2,
                "episode": 5,
                "position": 2760,
                "duration": 2760,
                "at": 1759140000,
            })
        );
    }

    #[test]
    fn a_cleared_mark_on_a_film_carries_position_zero_and_no_numbers() {
        let film = Identity {
            aliases: [("tmdb".to_string(), "7001".to_string())].into(),
            ..Identity::default()
        };
        let message: Value = serde_json::from_slice(&payload(
            TitleMark::Cleared,
            "den",
            &[],
            &film,
            6_000,
            1_759_140_000,
        ))
        .expect("the mark is JSON");

        assert_eq!(message["mark"], "cleared");
        assert_eq!(message["position"], 0);
        assert_eq!(message["duration"], 6000);
        assert_eq!(message["season"], 0);
        assert_eq!(message["episode"], 0);
        assert_eq!(message["people"], serde_json::json!([]));
    }
}
