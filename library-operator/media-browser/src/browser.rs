// The media browser: a stack of levels over a catalog source and a
// poster store. The keyboard and the bus fold through one key handler.
// The libraries level is always the bottom of the stack, so the screen
// is never empty, and back from that level asks for the shade. The
// `media-screen` crate holds the shade, the focus gate, and the two
// windows, so a press that reaches this file is one to act on.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::{Space, canvas};
use iced_winit::core::{Color, Element, Length, Theme};

use media_screen::{Bus, Moment};

use crate::bus::play;
use crate::catalog::{Selection, Source};
use crate::focus;
use crate::harness::{Screen, Waker};
use crate::levels::{Fetch, Level, Shape, shapes_of};
use crate::look;
use crate::posters::Posters;
use crate::views::list::List;
use crate::views::wall::{self, Wall};

/// The browsing screen, generic over where its rows and its posters
/// come from, so one browser draws the sidecar's file, a test fixture, and
/// the sample the same way.
pub struct Browser<S: Source, P: Posters> {
    source: S,
    // The store is in a RefCell because a canvas program draws through
    // a shared reference while the store mutates its cache.
    posters: RefCell<P>,
    // The libraries level is a field of its own, so the type guarantees
    // a level to draw; the stack holds only descents.
    libraries: Level,
    stack: Vec<Level>,
    // The connection to the room's remotes, or nothing on a run that takes
    // the keyboard alone.
    bus: Option<Box<dyn Bus>>,
    // The topic this operator reads play requests on. The browser
    // publishes on the connection the crate already holds, so the topic
    // is held here and not in the crate, which reads none of it.
    play_topic: String,
    // Whether the shade is down. The browser never decides it: it asks for
    // the shade, the crate decides, and the moment comes back here.
    asleep: bool,
    // Whether a present asked for a fresh Wayland surface.
    surface_due: bool,
}

impl<S: Source, P: Posters> Browser<S, P> {
    /// Open the browser on its first screen, the libraries.
    pub fn new(mut source: S, posters: P) -> Self {
        let libraries = Level::new(Shape::List, Fetch::Libraries, &mut source);
        Self {
            source,
            posters: RefCell::new(posters),
            libraries,
            stack: Vec::new(),
            bus: None,
            play_topic: String::new(),
            asleep: false,
            surface_due: false,
        }
    }

    /// The browser on a bus, and the topic it publishes a play request
    /// on. A `Player` whose status names no bus wires none, and the
    /// browser then takes the keyboard alone.
    pub fn with_bus(mut self, bus: Option<Box<dyn Bus>>, play_topic: String) -> Self {
        self.bus = bus;
        self.play_topic = play_topic;
        self
    }

    /// Whether the shade is down. The frame is black while it is.
    pub fn asleep(&self) -> bool {
        self.asleep
    }

    // Fold one moment in. A press is a key by another route, and the
    // crate already applied the focus gate and the play gate, so a
    // press that arrives here is one to act on. A press the browser
    // binds no key for changes nothing.
    fn receive(&mut self, moment: Moment) {
        match moment {
            Moment::Press(name) => {
                if let Some(key) = key_of(name) {
                    self.key(key);
                }
            }
            Moment::Sleep => self.asleep = true,
            Moment::Wake => self.asleep = false,
            Moment::Present => self.surface_due = true,
            // The browser draws no identity block, no unit status, and
            // no volume row, so these three change nothing here.
            Moment::Focus { .. } | Moment::Status(_) | Moment::Level { .. } => {}
        }
    }

    // Fold everything the bus delivered since the last wake. The answer is
    // whether anything folded.
    fn drain_bus(&mut self) -> bool {
        let moments = match &self.bus {
            Some(bus) => bus.drain(),
            None => return false,
        };
        let folded = !moments.is_empty();
        for moment in moments {
            self.receive(moment);
        }
        folded
    }

    fn top(&self) -> &Level {
        self.stack.last().unwrap_or(&self.libraries)
    }

    fn top_mut(&mut self) -> &mut Level {
        self.stack.last_mut().unwrap_or(&mut self.libraries)
    }

    fn reread_top(&mut self) {
        match self.stack.last_mut() {
            Some(top) => top.reread(&mut self.source),
            None => self.libraries.reread(&mut self.source),
        }
    }

    // A select on the focused row does one of two things, and the
    // kind's table decides which. Where the kind has a level below this
    // one, the descent pushes it. Where it has none, the row is a movie
    // or an episode, and the select is the play request.
    fn descend(&mut self) {
        let top = self.top();
        let Some(row) = top.rows.get(top.focus) else {
            return;
        };
        // What a select on the deepest row of a kind chose. A movie and
        // an episode have nothing below them, so the descent that finds
        // no next level is the play request.
        let mut chosen = None;
        let next = match &top.fetch {
            Fetch::Libraries => Some((
                shapes_of(&row.kind)[0],
                Fetch::Titles {
                    library: row.id.clone(),
                    kind: row.kind.clone(),
                },
            )),
            Fetch::Titles { library, kind } => match shapes_of(kind).get(1) {
                Some(shape) => Some((
                    *shape,
                    Fetch::Seasons {
                        library: library.clone(),
                        kind: kind.clone(),
                        series: row.id.clone(),
                    },
                )),
                None => {
                    chosen = Some((library.clone(), Selection::Movie { id: row.id.clone() }));
                    None
                }
            },
            Fetch::Seasons {
                library,
                kind,
                series,
            } => shapes_of(kind).get(2).map(|shape| {
                (
                    *shape,
                    Fetch::Episodes {
                        library: library.clone(),
                        series: series.clone(),
                        // Season rows mint their ids from the season
                        // number, so the parse cannot fail on rows this
                        // program built.
                        season: row.id.parse().unwrap_or(0),
                    },
                )
            }),
            Fetch::Episodes {
                library,
                series,
                season,
            } => {
                chosen = Some((
                    library.clone(),
                    Selection::Episode {
                        series: series.clone(),
                        season: *season,
                        episode: row.number,
                    },
                ));
                None
            }
        };
        if let Some((library, selection)) = chosen {
            self.request_play(&library, &selection);
            return;
        }
        if let Some((shape, fetch)) = next {
            let level = Level::new(shape, fetch, &mut self.source);
            self.stack.push(level);
        }
    }

    // Resolve the choice through the catalog and publish it. The browser
    // resolves the list because it holds the catalog, and the operator
    // creates the `Play` because it holds the credential. A choice with
    // no main file starts nothing, and the line in the pod log is the
    // only sign of the gap.
    fn request_play(&mut self, library: &str, selection: &Selection) {
        let items = self.source.play(library, selection);
        if items.is_empty() {
            eprintln!(
                "media-browser: no file to play for {} in {library}",
                selection.named()
            );
            return;
        }
        let Some(bus) = &self.bus else {
            return;
        };
        // An older library operator names no topic, and the browser
        // then browses and starts nothing. The line in the pod log is
        // the only sign of the gap.
        if self.play_topic.is_empty() {
            eprintln!("media-browser: no play topic, so this browser starts nothing");
            return;
        }
        // A request is an event, so it is not retained: a broker that
        // held the last one would replay it to the operator on every
        // reconnect.
        bus.publish(&self.play_topic, play::payload(library, &items), false);
    }

    // Back pops one descent and re-reads the level it uncovers,
    // because a change that landed while the level was covered was folded
    // into the level that was shown at the time and not into this one.
    //
    // At the libraries there is nowhere to climb, so a browser on a bus
    // asks for the shade. Only the browser knows whether back has
    // anywhere to go, which is why the crate never sleeps on back
    // itself.
    fn back(&mut self) {
        if self.stack.pop().is_some() {
            self.reread_top();
            return;
        }
        if let Some(bus) = &self.bus {
            bus.sleep();
        }
    }
}

impl<S: Source, P: Posters> Screen for Browser<S, P> {
    // Nothing on the screen emits a message; a remote's presses
    // arrive as keys, and the type says so.
    type Message = Infallible;

    // The shade means dark, so a sleeping browser clears to black and
    // not to the theme ground.
    fn background(&self) -> Color {
        if self.asleep {
            return Color::BLACK;
        }
        look::BACKGROUND
    }

    fn key(&mut self, name: &str) {
        match name {
            "enter" => self.descend(),
            "escape" | "backspace" => self.back(),
            _ => {
                let top = self.top_mut();
                top.focus = match top.shape {
                    Shape::Wall => focus::wall(top.focus, top.rows.len(), wall::COLUMNS, name),
                    Shape::List => focus::list(top.focus, top.rows.len(), name),
                };
            }
        }
    }

    // A poster that landed changes the frame and not the rows, so a
    // delivery redraws what is already read and only a changed source
    // re-reads the level.
    fn pump(&mut self, _at: f64) -> bool {
        let folded = self.drain_bus();
        let delivered = self.posters.get_mut().delivered();
        if !self.source.changed() {
            return folded || delivered;
        }
        self.reread_top();
        true
    }

    // The source, the poster store, and the bus deliver on threads of
    // their own, so all three take the handle that wakes the loop.
    fn wake_by(&mut self, wake: Waker) {
        self.source.wake_by(wake.clone());
        if let Some(bus) = &self.bus {
            bus.wake_on_delivery(wake.clone());
        }
        self.posters.get_mut().wake_by(wake);
    }

    fn surface_due(&mut self) -> bool {
        std::mem::take(&mut self.surface_due)
    }

    fn tick(&mut self, _at: f64) {}

    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer> {
        // The shade is down, so the frame is the clear color and nothing over
        // it. The level and its focus are held for the wake.
        if self.asleep {
            return Space::new().width(Length::Fill).height(Length::Fill).into();
        }

        let top = self.top();
        match top.shape {
            Shape::Wall => canvas(Wall {
                rows: &top.rows,
                focus: top.focus,
                library: top.fetch.library(),
                posters: &self.posters,
            })
            .width(Length::Fill)
            .height(Length::Fill)
            .into(),
            Shape::List => canvas(List {
                rows: &top.rows,
                focus: top.focus,
                library: top.fetch.library(),
                posters: &self.posters,
            })
            .width(Length::Fill)
            .height(Length::Fill)
            .into(),
        }
    }

    // Every view here is still until something changes it, and the
    // source wakes the loop itself, so an idle browser schedules nothing
    // and the loop waits on events.
    fn next_frame(&self, _at: f64) -> Option<f64> {
        None
    }
}

/// One kernel key name as the browser key it is. Several names reach one
/// key, because remotes differ in the name they send for OK and for back.
/// Select is enter and back is escape, so a press from a remote takes the
/// path the keyboard and the script take.
fn key_of(name: &str) -> Option<&'static str> {
    match name {
        "KEY_UP" => Some("up"),
        "KEY_DOWN" => Some("down"),
        "KEY_LEFT" => Some("left"),
        "KEY_RIGHT" => Some("right"),
        "KEY_ENTER" | "KEY_OK" | "KEY_SELECT" | "KEY_KPENTER" => Some("enter"),
        "KEY_BACK" | "KEY_ESC" | "KEY_EXIT" => Some("escape"),
        _ => None,
    }
}

#[cfg(test)]
mod tests;
