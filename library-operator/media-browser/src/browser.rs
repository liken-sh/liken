// The media browser: a stack of levels over a catalog source and a poster
// store. The keyboard and the bus fold through one key handler. The libraries
// level is always the bottom of the stack, so the screen is never empty, and
// back from that level asks the idle command pod for the shade.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::{Space, canvas};
use iced_winit::core::{Color, Element, Length, Theme};

use crate::bus::screen::Event;
use crate::bus::{Bus, Message};
use crate::catalog::Source;
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
    // Whether the shade is down. The browser never decides it, because the
    // shade is the idle command pod's alone; it reads the moment and draws.
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
            asleep: false,
            surface_due: false,
        }
    }

    /// The browser on a bus. A `Player` whose status names no bus wires none,
    /// and the browser then takes the keyboard alone.
    pub fn with_bus(mut self, bus: Option<Box<dyn Bus>>) -> Self {
        self.bus = bus;
        self
    }

    /// Whether the shade is down. The frame is black while it is.
    pub fn asleep(&self) -> bool {
        self.asleep
    }

    // Fold one message in. A press is a key by another route, and the moments
    // are the idle command pod's decisions about the screen.
    fn receive(&mut self, message: Message) {
        match message {
            Message::Press(name) => self.key(name),
            Message::Screen(Event::Sleep) => self.asleep = true,
            Message::Screen(Event::Wake) => self.asleep = false,
            Message::Screen(Event::Present) => self.surface_due = true,
            // The browser draws no identity block, so a focus pulses nothing.
            Message::Screen(Event::Focus { .. }) => {}
        }
    }

    // Fold everything the bus delivered since the last wake. The answer is
    // whether anything folded.
    fn drain_bus(&mut self) -> bool {
        let messages = match &self.bus {
            Some(bus) => bus.drain(),
            None => return false,
        };
        let folded = !messages.is_empty();
        for message in messages {
            self.receive(message);
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

    // A descent pushes the next level of the focused row, and the
    // next level is a fact of the kind's table: the shape at the next depth,
    // or nothing when the row is as deep as its kind goes.
    fn descend(&mut self) {
        let top = self.top();
        let Some(row) = top.rows.get(top.focus) else {
            return;
        };
        let next = match &top.fetch {
            Fetch::Libraries => Some((
                shapes_of(&row.kind)[0],
                Fetch::Titles {
                    library: row.id.clone(),
                    kind: row.kind.clone(),
                },
            )),
            Fetch::Titles { library, kind } => shapes_of(kind).get(1).map(|shape| {
                (
                    *shape,
                    Fetch::Seasons {
                        library: library.clone(),
                        kind: kind.clone(),
                        series: row.id.clone(),
                    },
                )
            }),
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
            Fetch::Episodes { .. } => None,
        };
        if let Some((shape, fetch)) = next {
            let level = Level::new(shape, fetch, &mut self.source);
            self.stack.push(level);
        }
    }

    // Back pops one descent and re-reads the level it uncovers,
    // because a change that landed while the level was covered was folded
    // into the level that was shown at the time and not into this one.
    //
    // At the libraries there is nowhere to climb, so a browser on a bus asks
    // the idle command pod for the shade. It never sleeps itself.
    fn back(&mut self) {
        if self.stack.pop().is_some() {
            self.reread_top();
            return;
        }
        if let Some(bus) = &self.bus {
            bus.request_sleep();
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

#[cfg(test)]
mod tests;
