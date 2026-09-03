// The media browser: a stack of screens over a catalog source and a
// poster store. The keyboard and the bus fold through one key handler.
// The libraries screen is always the bottom of the stack, so the screen
// is never empty, and back from that screen asks for the shade. The
// `media-screen` crate holds the shade, the focus gate, and the two
// windows, so a press that reaches this file is one to act on.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::Space;
use iced_winit::core::{Color, Element, Length, Theme};

use media_screen::{Bus, Moment};

use crate::bus::play;
use crate::catalog::{Selection, Source};
use crate::harness::{Screen, Waker};
use crate::look;
use crate::posters::Posters;
use crate::screens::{self, Step, libraries};

/// The browsing screen, generic over where its rows and its posters
/// come from, so one browser draws the sidecar's file, a test fixture, and
/// the sample the same way.
pub struct Browser<S: Source, P: Posters> {
    source: S,
    // The store is in a RefCell because a canvas program draws through
    // a shared reference while the store mutates its cache.
    posters: RefCell<P>,
    // The libraries screen is a field of its own, so the type guarantees
    // a screen to draw; the stack holds only descents.
    libraries: screens::Screen,
    stack: Vec<screens::Screen>,
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
    // The size a page's backdrop is decoded at, which is the size of the
    // window.
    page: (u32, u32),
    // The second of the last frame. The rest is measured from it.
    clock: f64,
    // The second at which the focused item's backdrop is asked for, or
    // nothing while focus moves or after the ask is spent.
    rest: Option<f64>,
}

// How long focus stands still before the browser asks the store for the
// backdrop of the page under it. A press inside this window replaces the
// ask, so a walk across a wall decodes the backdrop of the item a person
// stopped on and no other.
const REST: f64 = 0.3;

// The size a run that asked for no window size decodes a backdrop at.
const PAGE: (u32, u32) = (1920, 1080);

impl<S: Source, P: Posters> Browser<S, P> {
    /// Open the browser on its first screen, the libraries.
    pub fn new(mut source: S, posters: P) -> Self {
        let libraries = screens::Screen::Libraries(libraries::Libraries::new(&mut source));
        Self {
            source,
            posters: RefCell::new(posters),
            libraries,
            stack: Vec::new(),
            bus: None,
            play_topic: String::new(),
            asleep: false,
            surface_due: false,
            page: PAGE,
            clock: 0.0,
            rest: None,
        }
    }

    /// The browser on a window of this size. A page's backdrop is decoded
    /// at this size, so the decode the wall asked for is the one the page
    /// draws.
    pub fn with_page(mut self, page: (u32, u32)) -> Self {
        self.page = page;
        self
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

    fn top(&self) -> &screens::Screen {
        self.stack.last().unwrap_or(&self.libraries)
    }

    fn reread_top(&mut self) {
        let top = self.stack.last_mut().unwrap_or(&mut self.libraries);
        top.reread(&mut self.source);
        top.volume(&*self.posters.borrow());
    }

    // Do what the screen that took the press asked for. Only the browser
    // holds the stack, so a screen names the screen it opens and never
    // pushes one itself.
    fn take(&mut self, step: Step) {
        match step {
            Step::Stay => {}
            Step::Open(screen) => self.opened(screen),
            Step::Replace(screen) => {
                self.stack.pop();
                self.opened(screen);
            }
            Step::Play { library, selection } => self.request_play(&library, &selection),
        }
    }

    // Push a screen and read the files it draws off the volume,
    // which the screen itself cannot reach: only the browser holds the
    // store that resolves a library's root.
    fn opened(&mut self, mut screen: screens::Screen) {
        screen.volume(&*self.posters.borrow());
        self.stack.push(screen);
    }

    // Ask the store for the backdrop of the page under the focused item,
    // at the size the page draws it. The answer is dropped. The ask is
    // the point: the decode lands in the cache before the page opens.
    fn prefetch(&mut self) {
        let top = self.stack.last().unwrap_or(&self.libraries);
        let Some((library, art)) = top.resting(&mut self.source) else {
            return;
        };
        let (width, height) = self.page;
        let _ = self.posters.get_mut().poster(&library, &art, width, height);
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

    // Back pops one descent and re-reads the screen it uncovers,
    // because a change that landed while that screen was covered was
    // folded into the screen that was shown at the time and not into
    // this one.
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
        if name == "escape" || name == "backspace" {
            self.back();
        } else {
            let top = self.stack.last_mut().unwrap_or(&mut self.libraries);
            let step = top.key(name, &mut self.source);
            self.take(step);
        }
        // Every press starts the rest again, so the store decodes the
        // backdrop of the item a person stopped on and not of every item
        // focus passed over.
        self.rest = self.top().prefetches().then_some(self.clock + REST);
    }

    // A poster that landed changes the frame and not the rows, so a
    // delivery redraws what is already read and only a changed source
    // re-reads the screen.
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

    // The clock is read here alone, so the rest is measured on the same
    // clock the harness drives every frame with.
    fn tick(&mut self, at: f64) {
        self.clock = at;
        if self.rest.is_some_and(|due| at >= due) {
            self.rest = None;
            self.prefetch();
        }
    }

    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer> {
        // The shade is down, so the frame is the clear color and nothing over
        // it. The screen and its focus are held for the wake.
        if self.asleep {
            return Space::new().width(Length::Fill).height(Length::Fill).into();
        }

        self.top().view(&self.posters)
    }

    // Every view here is still until something changes it, and the
    // source wakes the loop itself, so an idle browser schedules nothing
    // and the loop waits on events. The one frame it schedules is the end
    // of a rest, and that frame is spent the moment the clock reaches it.
    fn next_frame(&self, _at: f64) -> Option<f64> {
        self.rest
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
