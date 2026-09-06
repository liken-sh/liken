// The keys this crate answers itself while nothing plays, beside the
// playback pod's table in `media-operator`'s `keybindings.go`. The two
// share the level keys and the cycle key. This crate owns those four
// because no client draws a list for them: the cycle key is answered
// for the operator, and the level keys step a state the crate holds.
// Every other key passes through to the client under the kernel's
// name, and the client binds it. The crate holds no table of the keys
// a client may want, because a remote with a keyboard sends letters,
// and only the client knows what a letter does on its screen.

use super::press::Press;
use crate::volume::{STEP, Volume};

/// The key that asks the operator to move the focus mark to the next unit. It
/// is the same name during a film and between films.
pub const CYCLE: &str = "KEY_CYCLEWINDOWS";

/// The three keys that step the level. They are named once here, so the
/// level rule and the owned check read the same names.
pub const VOLUME_UP: &str = "KEY_VOLUMEUP";
pub const VOLUME_DOWN: &str = "KEY_VOLUMEDOWN";
pub const MUTE: &str = "KEY_MUTE";

/// The three back synonyms. A shell sends whichever one it was built
/// with, so a client reads all three. This crate never sleeps the
/// screen on a press: only the client knows whether back has anywhere
/// to go, and the client asks for the shade with
/// [`super::Screen::sleep`].
pub const BACK: [&str; 3] = ["KEY_BACK", "KEY_ESC", "KEY_EXIT"];

/// Whether this crate acts on the key itself. This is the one check
/// that keeps a key from the client; every key it refuses passes
/// through.
pub fn owned(key: &str) -> bool {
    matches!(key, CYCLE | VOLUME_UP | VOLUME_DOWN | MUTE)
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
        VOLUME_UP => Some(held.stepped(STEP)),
        VOLUME_DOWN => Some(held.stepped(-STEP)),
        MUTE if press.down() => Some(held.toggled()),
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
    fn the_cycle_key_and_the_level_keys_are_this_crates_own() {
        for key in [CYCLE, VOLUME_UP, VOLUME_DOWN, MUTE] {
            assert!(owned(key));
        }
    }

    #[test]
    fn a_key_this_crate_acts_on_no_further_is_none_of_its_own() {
        for key in [
            "KEY_UP",
            "KEY_ENTER",
            "KEY_BACK",
            "KEY_A",
            "KEY_HOMEPAGE",
            "KEY_BACKSPACE",
            "KEY_PLAYPAUSE",
            "",
        ] {
            assert!(!owned(key));
        }
    }

    #[test]
    fn the_three_back_synonyms_are_the_clients_to_answer() {
        for key in BACK {
            assert!(back(key));
            assert!(!owned(key));
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
