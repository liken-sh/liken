// The keys this crate answers itself while nothing plays, beside the
// playback pod's table in `media-operator`'s `keybindings.go`. The two
// share the cycle key. This crate owns it because no client draws a
// list for it: the cycle key is answered for the operator. The volume
// keys belong to neither table. `media-operator` reads them off the
// remote's events topic and sets the level of the unit's devices, and a
// screen draws the level the operator relays. Every other key passes
// through to the client under the kernel's name, and the client binds
// it. The crate holds no table of the keys a client may want, because a
// remote with a keyboard sends letters, and only the client knows what
// a letter does on its screen.

/// The key that asks the operator to move the focus mark to the next unit. It
/// is the same name during a film and between films.
pub const CYCLE: &str = "KEY_CYCLEWINDOWS";

/// The three back synonyms. A shell sends whichever one it was built
/// with, so a client reads all three. This crate never sleeps the
/// screen on a press: only the client knows whether back has anywhere
/// to go, and the client asks for the shade with
/// [`super::Screen::sleep`].
pub const BACK: [&str; 3] = ["KEY_BACK", "KEY_ESC", "KEY_EXIT"];

/// The three power synonyms. A shell sends whichever one it was built
/// with, so a client reads all three. A unit whose screen is wired
/// through a Receiver answers a power press itself as a toggle on the
/// bus; a unit that is not forwards the key, and the client lowers its
/// shade. `media-operator` holds a copy of this list as `powerKeys` in
/// `media-operator/ensure.go`, because a Rust list cannot reach Go: a
/// change to one changes both.
pub const POWER: [&str; 3] = [POWER_PRESS, POWER_OFF, "KEY_POWER2"];

/// The two power synonyms that toggle the room. A Bluetooth remote's power
/// button and a TV remote's Power Toggle Function send one of them.
pub const TOGGLE: [&str; 2] = [POWER_PRESS, "KEY_POWER2"];

/// The key the kernel's `rc-cec` keymap names a TV remote's Power Off
/// Function. HDMI-CEC 1.3a, CEC 13.13.3, says it puts the device in standby
/// and keeps it there when repeated, so it asks the room for off and never
/// toggles.
pub const POWER_OFF: &str = "KEY_SLEEP";

/// The key the kernel's `rc-cec` keymap names a TV remote's Power On
/// Function. It puts the device on and keeps it on when repeated, so it
/// asks the room for on. It is no power synonym for a unit with no
/// Receiver: there it wakes a sleeping screen the way any press does.
pub const POWER_ON: &str = "KEY_WAKEUP";

/// The key name a power ask reaches the client under, on a unit with no
/// Receiver. The playback pod publishes the ask on the `Player`'s commands
/// topic during a film, and this crate turns it into a press once the unit
/// is idle, so a client binds the power names alone.
pub const POWER_PRESS: &str = "KEY_POWER";

/// The key name a home ask reaches the client under. The playback pod
/// publishes the ask on the `Player`'s commands topic during a film, and
/// this crate turns it into a press, so a client binds one name for home
/// whether the press came off a remote or out of an ask.
pub const HOME: &str = "KEY_HOMEPAGE";

/// Whether this crate acts on the key itself. This is the one check
/// that keeps a key from the client; every key it refuses passes
/// through.
pub fn owned(key: &str) -> bool {
    key == CYCLE
}

/// Whether one kernel key name is a back synonym.
pub fn back(key: &str) -> bool {
    BACK.contains(&key)
}

/// Whether one kernel key name is a power synonym.
pub fn power(key: &str) -> bool {
    POWER.contains(&key)
}

/// The ask a power key publishes on the power topic of a unit with a
/// Receiver: off and on for the two deterministic functions, the toggle for
/// every other power key, and nothing for a key that is no power key.
pub fn power_action(key: &str) -> Option<&'static str> {
    match key {
        POWER_OFF => Some("off"),
        POWER_ON => Some("on"),
        _ if TOGGLE.contains(&key) => Some("toggle"),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_cycle_key_is_this_crates_own() {
        assert!(owned(CYCLE));
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
            "KEY_VOLUMEUP",
            "KEY_VOLUMEDOWN",
            "KEY_MUTE",
            "KEY_UNMUTE",
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
    fn the_three_power_synonyms_are_none_of_the_crates_own() {
        for key in POWER {
            assert!(power(key));
            assert!(!owned(key));
        }
        assert!(!power("KEY_UP"));
        assert!(!power("KEY_BACK"));
    }

    #[test]
    fn each_power_key_names_its_ask() {
        assert_eq!(power_action("KEY_POWER"), Some("toggle"));
        assert_eq!(power_action("KEY_POWER2"), Some("toggle"));
        assert_eq!(power_action(POWER_OFF), Some("off"));
        assert_eq!(power_action(POWER_ON), Some("on"));
        assert_eq!(power_action("KEY_UP"), None);
    }
}
