// The keys this crate answers itself while nothing plays, beside the
// playback pod's table in `media-operator`'s `keybindings.go`. The two
// share the cycle key. This crate owns it because no client draws a
// list for it: the cycle key is answered for the operator. The volume
// keys belong to neither table. `media-operator` reads them off the
// remote's events topic and sets the level of the unit's devices, and a
// screen draws the level the operator relays. The power keys belong to
// no client either: `media-operator` reads them the same way and turns
// the `Player` off and on, and this crate drops them. Every other key passes
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

/// The four power keys: the two power synonyms a shell and a Bluetooth
/// remote send, which toggle, and the names the kernel's `rc-cec` keymap
/// gives a TV remote's Power Off Function and Power On Function (HDMI-CEC
/// 1.3a, CEC 13.13.3). `media-operator` reads them off the controller's
/// events topic and turns the `Player` off and on through its
/// `spec.power`, so this crate never acts on them and never hands them to
/// the client. `media-operator` holds the same four names in
/// `media-operator/powerkeys.go`, because a Rust list cannot reach Go: a
/// change to one changes both.
pub const POWER: [&str; 4] = ["KEY_POWER", "KEY_POWER2", "KEY_SLEEP", "KEY_WAKEUP"];

/// The key name a home ask reaches the client under. The playback pod
/// publishes the ask on the `Player`'s commands topic during a film, and
/// this crate turns it into a press, so a client binds one name for home
/// whether the press came off a remote or out of an ask.
pub const HOME: &str = "KEY_HOMEPAGE";

/// Whether this crate acts on the key itself. This check and [`power`]
/// keep a key from the client; every other key passes through.
pub fn owned(key: &str) -> bool {
    key == CYCLE
}

/// Whether one kernel key name is a back synonym.
pub fn back(key: &str) -> bool {
    BACK.contains(&key)
}

/// Whether one kernel key name is a power key.
pub fn power(key: &str) -> bool {
    POWER.contains(&key)
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
    fn the_four_power_keys_are_none_of_the_crates_own() {
        for key in POWER {
            assert!(power(key));
            assert!(!owned(key));
        }
        assert!(!power("KEY_UP"));
        assert!(!power("KEY_BACK"));
    }
}
