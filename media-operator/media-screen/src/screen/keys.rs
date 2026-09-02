// This crate's table from key names to what they do while nothing plays,
// beside the playback pod's table in `media-operator`'s `keybindings.go`. The
// two tables share the level keys and the cycle key. They part on navigation:
// the navigation keys reach the client that drew the list, and the playback
// pod's reach a display script.
//
// The crate holds a table rather than passing every key through,
// because three kinds of key are its own: the cycle key it answers for
// the operator, and the two level keys and mute, which step a state no
// client draws a list for. Every other key a screen client acts on is a
// navigation key, and the client binds it.

use super::press::Press;
use crate::volume::{STEP, Volume};

/// The key that asks the operator to move the focus mark to the next unit. It
/// is the same name during a film and between films.
pub const CYCLE: &str = "KEY_CYCLEWINDOWS";

/// The navigation keys a client answers: the arrows, the select synonyms, and
/// the back synonyms. Back is one of them, so this crate never sleeps the
/// screen on a press. Only the client knows whether back has anywhere to go,
/// and the client asks for the shade with [`super::Screen::sleep`].
pub const NAVIGATION: [&str; 11] = [
    "KEY_UP",
    "KEY_DOWN",
    "KEY_LEFT",
    "KEY_RIGHT",
    "KEY_ENTER",
    "KEY_OK",
    "KEY_SELECT",
    "KEY_KPENTER",
    "KEY_BACK",
    "KEY_ESC",
    "KEY_EXIT",
];

/// The three back synonyms among the navigation keys. A shell sends whichever
/// one it was built with, so a client that sleeps on back reads all three.
pub const BACK: [&str; 3] = ["KEY_BACK", "KEY_ESC", "KEY_EXIT"];

/// One kernel key name as the navigation key it is, and nothing for a key
/// this crate hands no client. The answer is the table's own name, so the
/// client reads the kernel's name for the control and holds its own table.
pub fn navigation(key: &str) -> Option<&'static str> {
    NAVIGATION.into_iter().find(|name| *name == key)
}

/// Whether one kernel key name is a back synonym.
pub fn back(key: &str) -> bool {
    BACK.contains(&key)
}

/// What one press means for the level, and nothing for a key that names no
/// level. The two steps act on the press and on the repeat, because a person
/// ramps a level by holding the key. Mute acts on the press alone, because a
/// held mute that toggled on every repeat would flip the flag back and forth
/// under the hand.
pub fn level(press: &Press, held: Volume) -> Option<Volume> {
    match press.key.as_str() {
        "KEY_VOLUMEUP" => Some(held.stepped(STEP)),
        "KEY_VOLUMEDOWN" => Some(held.stepped(-STEP)),
        "KEY_MUTE" if press.down() => Some(held.toggled()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn press(key: &str, value: i64) -> Press {
        Press {
            key: key.into(),
            value,
        }
    }

    #[test]
    fn every_navigation_key_reaches_the_client_under_its_own_name() {
        for key in NAVIGATION {
            assert_eq!(navigation(key), Some(key));
        }
    }

    #[test]
    fn a_key_this_crate_hands_no_client_is_no_navigation_key() {
        assert_eq!(navigation("KEY_PLAYPAUSE"), None);
        assert_eq!(navigation("KEY_VOLUMEUP"), None);
        assert_eq!(navigation(CYCLE), None);
        assert_eq!(navigation(""), None);
    }

    #[test]
    fn the_three_back_synonyms_are_navigation_keys_too() {
        for key in BACK {
            assert!(back(key));
            assert_eq!(navigation(key), Some(key));
        }
        assert!(!back("KEY_UP"));
    }

    #[test]
    fn the_level_keys_step_by_five_on_the_press_and_the_repeat() {
        let held = Volume {
            level: 40,
            muted: false,
        };
        for value in [1, 2] {
            assert_eq!(
                level(&press("KEY_VOLUMEUP", value), held),
                Some(Volume {
                    level: 45,
                    muted: false
                })
            );
            assert_eq!(
                level(&press("KEY_VOLUMEDOWN", value), held),
                Some(Volume {
                    level: 35,
                    muted: false
                })
            );
        }
    }

    #[test]
    fn mute_toggles_on_the_press_alone() {
        let held = Volume::default();
        assert_eq!(
            level(&press("KEY_MUTE", 1), held),
            Some(Volume {
                level: 100,
                muted: true
            })
        );
        assert_eq!(level(&press("KEY_MUTE", 2), held), None);
    }

    #[test]
    fn a_key_that_names_no_level_is_no_level_press() {
        assert_eq!(level(&press("KEY_UP", 1), Volume::default()), None);
        assert_eq!(level(&press(CYCLE, 1), Volume::default()), None);
    }
}
