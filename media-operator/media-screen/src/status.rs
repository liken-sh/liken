// The `Player`'s retained status: one message that carries the whole of what
// the operator says about a unit. The operator folds the Kubernetes objects
// into it, so a client resolves nothing and reads one payload.
//
// `media-operator` writes it in `playerstatus.go`.

use serde::Deserialize;

/// The status as it travels on the topic.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    /// The unit's friendly name. It replaces the whole identity block, so an
    /// edit to a `Player` shows with no pod restart.
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub activity: Activity,
    /// The `Play` that runs or starts on the unit. A unit at rest names none.
    #[serde(default)]
    pub play: Option<Play>,
    #[serde(default)]
    pub components: Vec<Component>,
}

/// What the unit is doing. The three words are the operator's own, and the
/// screen draws a different motion for each: `Starting` ramps the mark up
/// while a person waits, `Playing` stops it because a film covers the surface,
/// and `Idle` eases it back to rest.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Activity {
    #[default]
    Idle,
    Starting,
    Playing,
}

impl Activity {
    /// The activity one word names. A word this client does not name reads as
    /// `Idle`, which draws the mark at rest and no activity line, because
    /// every comparison downstream is against `Starting` and `Playing` alone.
    pub fn from_word(word: &str) -> Self {
        match word {
            "Starting" => Self::Starting,
            "Playing" => Self::Playing,
            _ => Self::Idle,
        }
    }
}

impl<'de> Deserialize<'de> for Activity {
    fn deserialize<D: serde::Deserializer<'de>>(source: D) -> Result<Self, D::Error> {
        Ok(Self::from_word(&String::deserialize(source)?))
    }
}

/// The `Play` on the unit. `name` is the object a person finds with `kubectl`,
/// and `title` is the one line the screen draws.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct Play {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub title: String,
}

/// One part of the unit: a screen, a set of speakers, or a controller.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct Component {
    /// A part that carries no name defaults to an empty one, and the
    /// identity block drops it, because one unnamed part must not cost
    /// the whole retained status and leave the screen on stale state.
    #[serde(default)]
    pub name: String,
    /// `display`, `sink`, or `remote`. It is the screen's whole vocabulary for
    /// a part, and it says what to draw rather than which `DeviceClass` the
    /// part came from.
    #[serde(default)]
    pub kind: String,
    /// The presence of a part that has any. A wired screen and its speakers
    /// report none and carry no key, and the screen draws them at full
    /// brightness always, because a part that cannot be absent must not read
    /// as present-for-now.
    #[serde(default)]
    pub connected: Option<bool>,
    /// The charge the part reports, from 0 to 100. A part that runs on no
    /// battery, and one whose device reports no level, carries no key, and the
    /// screen draws the name alone.
    #[serde(default)]
    pub battery: Option<i64>,
    /// True on the one controller whose focus mark names this unit. It appears
    /// nowhere else, so exactly one unit draws the marker for a controller that
    /// several units list.
    #[serde(default)]
    pub focused: Option<bool>,
}

impl Component {
    /// The kind name of a controller, the one part that carries presence and
    /// focus. A focus moment counts the controllers in the order the status
    /// lists them, which is the `Player`'s `spec.remotes` order.
    pub const REMOTE: &'static str = "remote";
}

/// Read one message off the status topic. A payload that does not decode is no
/// status at all and changes nothing, because a half-read status on a screen is
/// worse than the last good one.
pub fn parse(payload: &[u8]) -> Option<Status> {
    crate::object(payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_whole_status_decodes() {
        let status = parse(
            br#"{"displayName":"The Den","activity":"Starting",
                 "play":{"name":"den-tv-1","title":"A Film"},
                 "components":[
                   {"name":"The screen","kind":"display"},
                   {"name":"A remote","kind":"remote","connected":true,
                    "battery":62,"focused":true}]}"#,
        )
        .expect("the status decodes");

        assert_eq!(status.display_name, "The Den");
        assert_eq!(status.activity, Activity::Starting);
        assert_eq!(
            status.play.as_ref().map(|play| play.title.as_str()),
            Some("A Film")
        );
        assert_eq!(status.components[0].kind, "display");
        assert_eq!(status.components[0].connected, None);
        assert_eq!(status.components[1].connected, Some(true));
        assert_eq!(status.components[0].battery, None);
        assert_eq!(status.components[1].battery, Some(62));
        assert_eq!(status.components[1].focused, Some(true));
    }

    #[test]
    fn a_unit_at_rest_names_no_play_and_no_parts() {
        let status = parse(br#"{"displayName":"The Den","activity":"Idle"}"#).expect("it decodes");
        assert_eq!(status.activity, Activity::Idle);
        assert_eq!(status.play, None);
        assert!(status.components.is_empty());
    }

    #[test]
    fn a_word_this_client_does_not_name_is_idle() {
        assert_eq!(Activity::from_word("Playing"), Activity::Playing);
        assert_eq!(Activity::from_word("Buffering"), Activity::Idle);
        assert_eq!(Activity::from_word(""), Activity::Idle);
    }

    #[test]
    fn text_that_does_not_parse_is_no_status() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"{"), None);
        assert_eq!(parse(b"[]"), None);
        assert_eq!(parse(br#"["The Den","Idle",null,[]]"#), None);
    }

    #[test]
    fn a_part_with_no_name_leaves_the_rest_of_the_status_readable() {
        let status = parse(
            br#"{"displayName":"The Den","components":[
                   {"kind":"remote"},{"name":"A remote","kind":"remote"}]}"#,
        )
        .expect("the status decodes");

        assert_eq!(status.display_name, "The Den");
        assert_eq!(status.components[0].name, "");
        assert_eq!(status.components[1].name, "A remote");
    }
}
