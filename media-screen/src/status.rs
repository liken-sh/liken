// The `Player`'s retained status message, which `media-operator` writes in
// `playerstatus.go`. It combines the `Player`, its `Play`, and its devices
// into one payload, so a client needs no access to the API server.

use serde::Deserialize;

#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    /// The display name is in the status, so an edit to the `Player`
    /// reaches the screen without a pod restart.
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub activity: Activity,
    /// The `Play` that is starting or playing on the `Player`, if any.
    #[serde(default)]
    pub play: Option<Play>,
    #[serde(default)]
    pub components: Vec<Component>,
}

/// What the `Player` is doing.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Activity {
    #[default]
    Idle,
    Starting,
    Playing,
}

impl Activity {
    /// An unrecognized word becomes `Idle`.
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

/// `name` is the `Play`'s `metadata.name`, and `title` is the line the
/// screen draws.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct Play {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub title: String,
}

/// One device of the `Player`: its screen, a set of speakers, or a
/// controller. The operator lists the screen first, then the speakers, then
/// the controllers in `spec.remotes` order.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct Component {
    /// Without this default, one part with no name would fail the whole
    /// status, and the screen would keep showing the previous one.
    #[serde(default)]
    pub name: String,
    /// `display`, `sink`, or `remote`. This is what the screen draws, not
    /// the part's `DeviceClass`.
    #[serde(default)]
    pub kind: String,
    /// The operator sets this only on a controller whose connection state
    /// it reads. The screen draws a part without it at full brightness.
    #[serde(default)]
    pub connected: Option<bool>,
    /// The battery charge from 0 to 100. It is `None` for a part with no
    /// battery and for a device that reports no level, which is not the
    /// same as an empty battery.
    #[serde(default)]
    pub battery: Option<i64>,
    /// `Some(true)` on a controller whose focus topic names this `Player`,
    /// and absent otherwise. A controller that several `Player`s list is
    /// focused on at most one of them.
    #[serde(default)]
    pub focused: Option<bool>,
}

impl Component {
    /// The `kind` of a controller, the only part that has `connected`,
    /// `battery`, or `focused`. The `remote` index of a
    /// [`crate::Moment::Focus`] counts only the parts of this kind, in the
    /// order the status lists them.
    pub const REMOTE: &'static str = "remote";
}

/// Returns `None` for a payload that does not decode, and the screen keeps
/// the last good status.
pub fn parse(payload: &[u8]) -> Option<Status> {
    crate::object(payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_whole_status_decodes() {
        let status = parse(
            br#"{"displayName":"Player One","activity":"Starting",
                 "play":{"name":"player-1-1","title":"A Film"},
                 "components":[
                   {"name":"The screen","kind":"display"},
                   {"name":"A remote","kind":"remote","connected":true,
                    "battery":62,"focused":true}]}"#,
        )
        .expect("the status decodes");

        assert_eq!(status.display_name, "Player One");
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
    fn an_idle_status_with_no_play_or_components_decodes_to_none_and_empty() {
        let status =
            parse(br#"{"displayName":"Player One","activity":"Idle"}"#).expect("it decodes");
        assert_eq!(status.activity, Activity::Idle);
        assert_eq!(status.play, None);
        assert!(status.components.is_empty());
    }

    #[test]
    fn an_unrecognized_activity_word_is_idle() {
        assert_eq!(Activity::from_word("Playing"), Activity::Playing);
        assert_eq!(Activity::from_word("Buffering"), Activity::Idle);
        assert_eq!(Activity::from_word(""), Activity::Idle);
    }

    #[test]
    fn a_payload_that_is_not_a_json_object_is_no_status() {
        assert_eq!(parse(b""), None);
        assert_eq!(parse(b"{"), None);
        assert_eq!(parse(b"[]"), None);
        assert_eq!(parse(br#"["Player One","Idle",null,[]]"#), None);
    }

    #[test]
    fn a_component_with_no_name_still_decodes_the_rest_of_the_status() {
        let status = parse(
            br#"{"displayName":"Player One","components":[
                   {"kind":"remote"},{"name":"A remote","kind":"remote"}]}"#,
        )
        .expect("the status decodes");

        assert_eq!(status.display_name, "Player One");
        assert_eq!(status.components[0].name, "");
        assert_eq!(status.components[1].name, "A remote");
    }
}
