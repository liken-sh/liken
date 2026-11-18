// Kernel key names that this crate treats specially. Every other key passes
// to the client, because only the client defines what a key does on its
// screen. `media-operator` reads the volume and power keys from each
// controller's events topic and acts on them itself, so this crate drops
// the power keys and passes the volume keys to the client.

/// While the `Player` is idle, this crate turns a press of this key into a
/// request to `media-operator` to move the controller's focus to the next
/// `Player`. During a film, the playback pod handles the same key, as
/// `media-operator/keybindings.go` lists.
pub const CYCLE: &str = "KEY_CYCLEWINDOWS";

/// The keys that mean back. Remotes send different names for it, and the
/// media browser also reads `KEY_BACKSPACE` as back, except in search,
/// where it deletes a character.
///
/// Only the client can tell whether back has anywhere to go, so the client
/// calls [`super::Screen::sleep`], as the idle screen does on back. A back
/// press while the screen sleeps only wakes it and does not reach the
/// client, because the idle screen would put it to sleep again at once.
pub const BACK: [&str; 4] = ["KEY_BACK", "KEY_ESC", "KEY_EXIT", "KEY_BACKSPACE"];

/// `KEY_POWER` and `KEY_POWER2` toggle. `KEY_SLEEP` and `KEY_WAKEUP` are
/// the names that the kernel's `rc-cec` keymap gives the CEC Power Off
/// Function and Power On Function (HDMI-CEC 1.3a, CEC 13.13.3).
/// `media-operator` turns the `Player` off and on through `spec.power` on
/// these keys, so this crate never passes them to the client.
/// `media-operator/powerkeys.go` lists the same keys, so change both
/// together.
pub const POWER: [&str; 4] = ["KEY_POWER", "KEY_POWER2", "KEY_SLEEP", "KEY_WAKEUP"];

/// When the playback pod publishes a home request on the `Player`'s
/// commands topic, this crate passes it to the client as a press of this
/// key, so the client handles home the same way from a remote and from a
/// film.
pub const HOME: &str = "KEY_HOMEPAGE";

pub fn cycle(key: &str) -> bool {
    key == CYCLE
}

pub fn back(key: &str) -> bool {
    BACK.contains(&key)
}

pub fn power(key: &str) -> bool {
    POWER.contains(&key)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_cycle_key_cycles_focus() {
        assert!(cycle(CYCLE));
    }

    #[test]
    fn no_other_key_cycles_focus() {
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
            assert!(!cycle(key));
        }
    }

    #[test]
    fn every_back_key_name_means_back() {
        for key in ["KEY_BACK", "KEY_ESC", "KEY_EXIT", "KEY_BACKSPACE"] {
            assert!(back(key), "{key}");
            assert!(!cycle(key), "{key}");
        }
        assert!(!back("KEY_UP"));
    }

    #[test]
    fn every_power_key_name_is_a_power_key() {
        for key in POWER {
            assert!(power(key));
            assert!(!cycle(key));
        }
        assert!(!power("KEY_UP"));
        assert!(!power("KEY_BACK"));
    }
}
