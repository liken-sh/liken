// One press, from the keyboard or from a remote over the bus, and the
// line it leaves in the pod log. A press is the unit a person acts in,
// so each one prints exactly one line: the key, and what the browser
// did with it. The handlers below say what they did through `did`, and
// a press no handler describes is a move of focus, a search edit, or
// nothing.

use media_screen::status::Activity;

use super::{Browser, REST, lines};
use crate::art::Art;
use crate::catalog::Source;
use crate::screens::Step;
use crate::views;

impl<S: Source, A: Art> Browser<S, A> {
    // Act on one press and print its line. `kernel` is the name the
    // remote sent, where the press came over the bus.
    pub(super) fn press(&mut self, name: &str, kernel: Option<&str>) -> bool {
        // A handler that ran outside a press, such as the up-next ask,
        // may have left a description, and this press did not do that.
        self.did = None;
        let typed = self.typed();
        let changed = self.act(name);
        let did = self.did.take().unwrap_or_else(|| match self.typed() {
            Some(length) if self.typed() != typed => format!(
                "search input, and the field holds {}",
                lines::count(length, "character")
            ),
            _ if changed => "moved focus".to_string(),
            _ => "changed nothing".to_string(),
        });
        self.log
            .line(format!("press {}: {did}", lines::press(name, kernel)));
        changed
    }

    // How many characters the search field holds, or nothing where the
    // screen on top is not the search wall.
    fn typed(&self) -> Option<usize> {
        self.top().field().map(|field| field.text().chars().count())
    }

    // Record what the press did, for its line.
    pub(super) fn did(&mut self, what: impl Into<String>) {
        self.did = Some(what.into());
    }

    fn act(&mut self, name: &str) -> bool {
        // Every press holds the audience's answer open, whatever the press
        // then does, because a person at the remote is a person in the room.
        self.pressed();
        // A home press is answered ahead of the loading gate and the picker
        // gate, so a film that covered the browser ends over the home page
        // and not over the page a person chose it from. The playback pod
        // sends the press just before the film ends, and the loading state
        // stays for the return path to end.
        if name == "home" && (self.loading.is_some() || self.activity != Activity::Idle) {
            self.picker = None;
            self.home();
            return true;
        }
        // A press during the loading state reaches no screen under it.
        // Back exits the state here and now, and cancels nothing: the
        // `Play` this browser asked for is the operator's to run.
        if self.loading.is_some() {
            match name == "escape" || name == "backspace" {
                true => {
                    self.presented();
                    self.did("left the loading state");
                }
                false => self.did("changed nothing, because the loading state is on the screen"),
            }
            return true;
        }
        // The press that raises the picker does nothing else, so a room
        // that answered hours ago never plays under the last room's name.
        if self.ask() {
            self.did("raised the person picker, because nobody has said who is watching");
            return true;
        }
        if self.picker.is_some() {
            self.on_picker(name);
            return true;
        }
        let mut changed = true;
        match name {
            // Escape on the strip gives focus back to the screen and pops
            // nothing, because the strip is over the stack and not on it.
            "escape" if self.on_strip => self.leave_strip(),
            // The screen on top is asked first, because a search wall
            // reads backspace as a deleted character, and escape as the
            // grid closed or the text cleared. Every other screen takes
            // neither, and both words are then back.
            "escape" | "backspace" => {
                let top = self.stack.last_mut().unwrap_or(&mut self.home);
                match top.escape(name, &mut self.source) {
                    Some(step) => self.step(step),
                    None => self.back(),
                }
            }
            "home" => self.home(),
            // The people key raises the picker over whatever screen is
            // up, the way home pops to the home page. A press that arrives
            // while the picker stands never reaches here: the picker took
            // it above and binds no word for people, so it stands as it
            // was. A browser that knows no people has an empty picker to
            // draw and nothing to ask, so the key moves nothing there, the
            // way the ask never raises one.
            "people" => match self.audience.known().is_empty() {
                true => {
                    changed = false;
                    self.did("changed nothing, because the cluster holds no Person");
                }
                false => self.raise_picker(),
            },
            // The power key asks for the shade. The crate decides, and the
            // sleep moment comes back here; the press itself changes
            // nothing on the screen. A press that arrives asleep never
            // reaches here, because the crate wakes on it instead, so one
            // button is the shade down and the shade up.
            "power" => {
                changed = false;
                self.rest();
            }
            // The search key opens the empty wall with the grid shown, and
            // does nothing on a search wall.
            "search" => match self.top().searching() {
                true => changed = false,
                false => self.search("", true),
            },
            // A letter or a digit opens the search wall seeded with the
            // character and the grid hidden, because a person who typed a
            // letter has a keyboard. It happens here and not in a screen,
            // so every screen reaches search the same way. The wall is
            // pushed, so back returns to the screen the person left. On
            // the search wall the letter types.
            _ if views::field::typed(name) && !self.top().searching() => {
                self.search(name, false);
            }
            _ if self.on_strip => changed = self.on_strip(name),
            _ => changed = self.on_screen(name),
        }
        // Every press starts the rest again, so the store decodes the
        // backdrop of the item a person stopped on and not of every item
        // focus passed over. A strip that holds focus asks for nothing,
        // because no press there opens a page over art.
        self.rest = (!self.on_strip && self.top().prefetches()).then_some(self.clock + REST);
        changed
    }

    // One press on the person picker. A press that answers takes the
    // answer; one that changes who is chosen names the people now chosen,
    // by their `Person` name.
    fn on_picker(&mut self, name: &str) {
        let Some(picker) = &mut self.picker else {
            return;
        };
        let before = picked(picker);
        match picker.key(name) {
            Some(chosen) => self.answered(chosen),
            None => {
                let after = picked(picker);
                if after != before {
                    let names = self.names(&after);
                    self.did(format!(
                        "chose {} in the person picker",
                        lines::people(&names)
                    ));
                }
            }
        }
    }

    // The `Person` names at these indexes of the known list.
    pub(super) fn names(&self, chosen: &[usize]) -> Vec<String> {
        chosen
            .iter()
            .filter_map(|index| self.audience.known().get(*index))
            .map(|person| person.name.clone())
            .collect()
    }

    // Do what the screen on top asked for. Every step that changes the
    // stack names the screen it lands on, so the press's line says where
    // the person went.
    pub(super) fn step(&mut self, step: Step) {
        let replaced = matches!(step, Step::Replace(_));
        let opened = matches!(step, Step::Open(_));
        self.take(step);
        if opened || replaced {
            let verb = match replaced {
                true => "replaced the page with",
                false => "opened",
            };
            let top = lines::screen(self.top());
            self.did(format!("{verb} {top}"));
        }
    }
}

// The people a picker holds chosen, by their index in the known list.
fn picked(picker: &crate::screens::audience::Picker) -> Vec<usize> {
    (0..picker.tiles())
        .filter(|index| picker.holds(*index))
        .collect()
}
