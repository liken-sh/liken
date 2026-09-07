// The media browser: a stack of screens over a catalog source and an
// art store. The keyboard and the bus fold through one key handler.
// The home page is always the bottom of the stack, so the screen is never
// empty, and back from the home page asks for the shade. The
// `media-screen` crate holds the shade, the focus gate, and the two
// windows, so a press that reaches this file is one to act on.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::{Space, Stack, canvas};
use iced_winit::core::{Color, Element, Length, Theme};

use media_screen::{Bus, Moment};

use crate::art::{Art, ArtCounts};
use crate::bus::play;
use crate::catalog::draw::Date;
use crate::catalog::search::Size;
use crate::catalog::{Selection, Source};
use crate::clock;
use crate::harness::{Screen, Waker};
use crate::look;
use crate::screens::{self, Step, home, loading, volume};
use crate::views;

mod keys;
mod reader;
// The stack module: the screens a person descended through, and every
// move across them.
mod stack;

use keys::key_of;

/// The browsing screen, generic over where its rows and its art
/// come from, so one browser draws the sidecar's file, a test fixture, and
/// the sample the same way.
pub struct Browser<S: Source, A: Art> {
    source: S,
    // The store is in a RefCell because a canvas program draws through
    // a shared reference while the store mutates its cache.
    store: RefCell<A>,
    // The home page is a field of its own, so the type guarantees a
    // screen to draw; the stack holds only descents.
    home: screens::Screen,
    stack: Vec<screens::Screen>,
    // Where the home page's reads run. Every read after the first goes
    // through here, so the frame thread draws while the read runs.
    reader: reader::Reader,
    // Whether the home page is behind the catalog. A change the source
    // reports marks it, whether or not a page covers the home page,
    // because back pops to the home page with no read of its own.
    home_stale: bool,
    // The date the home page was read on. The day's draw is seeded by
    // the date, so a page read yesterday is behind today.
    home_date: Date,
    // Where the date comes from. It is a field so a test moves the day
    // without the wall clock.
    today: fn() -> Date,
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
    // The loading state the page under a chosen title is in, or nothing
    // while no title has been chosen.
    loading: Option<loading::Loading>,
    // The volume row's state, which the level moments the bus delivers fold
    // into.
    level: volume::Level,
    // The reading the strip draws, read at every tick.
    time: clock::Time,
    // Whether the strip over every screen holds focus. It is the
    // browser's field and not a screen's, because the strip is the
    // browser's layer, and it clears whenever the stack changes.
    on_strip: bool,
    // The second on the loop's own clock at which the minute turns, or
    // nothing before the first tick. The clock draws a reading to the
    // minute, so this is the one frame it asks for.
    minute: Option<f64>,
}

// How long focus stands still before the browser asks the store for the
// backdrop of the page under it. A press inside this window replaces the
// ask, so a walk across a wall decodes the backdrop of the item a person
// stopped on and no other.
const REST: f64 = 0.3;

// The size a run that asked for no window size decodes a backdrop at.
const PAGE: (u32, u32) = (1920, 1080);

impl<S: Source, A: Art> Browser<S, A> {
    /// Open the browser on its first screen, the home page.
    pub fn new(mut source: S, store: A) -> Self {
        let home_date = Date::today();
        // The first read is the one a person waits for, so the run says
        // how long it took, the way the reader thread says it of a re-read.
        let started = std::time::Instant::now();
        let home = screens::Screen::Home(home::Home::open(&mut source));
        let ms = started.elapsed().as_secs_f64() * 1_000.0;
        eprintln!("media-browser: the home page opened in {ms:.1} ms");
        let reader = reader::Reader::new(source.reader());
        Self {
            source,
            store: RefCell::new(store),
            home,
            stack: Vec::new(),
            reader,
            home_stale: false,
            home_date,
            today: Date::today,
            bus: None,
            play_topic: String::new(),
            asleep: false,
            surface_due: false,
            page: PAGE,
            clock: 0.0,
            rest: None,
            loading: None,
            level: volume::Level::default(),
            time: clock::now(),
            on_strip: false,
            minute: None,
        }
    }

    /// The browser on a window of this size. A page's backdrop is decoded
    /// at this size, so the decode the wall asked for is the one the page
    /// draws.
    pub fn with_page(mut self, page: (u32, u32)) -> Self {
        self.page = page;
        self
    }

    /// The browser that prints the milliseconds each home read takes,
    /// which the measured run asks for.
    pub fn with_timing(self, timed: bool) -> Self {
        self.reader.timed(timed);
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
                if let Some(key) = key_of(&name) {
                    self.key(key);
                }
            }
            Moment::Sleep => self.asleep = true,
            Moment::Wake => {
                self.asleep = false;
                self.presented();
                self.lifted();
            }
            Moment::Present => {
                self.surface_due = true;
                self.presented();
                self.lifted();
            }
            // A level brings up the volume row, which draws over every
            // screen.
            Moment::Level { volume, pressed } => self.level.fold(volume, pressed, self.clock),
            // The browser draws no identity block and no unit status, so
            // these two change nothing here.
            Moment::Focus { .. } | Moment::Status(_) => {}
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

    // Ask the reader for the home page, but only where the page is
    // behind: a change the source reported, or a day other than the one
    // it was read on. A read in place lands on this call, and a read on
    // the thread lands on a later pass.
    fn refresh_home(&mut self) {
        let today = (self.today)();
        if !self.home_stale && self.home_date == today {
            return;
        }
        self.reader.ask(&mut self.source, today);
        self.landed_home();
    }

    // Take the page the reader answered, where one landed. The answer is
    // whether the home page changed, which is a frame to draw.
    fn landed_home(&mut self) -> bool {
        let Some(page) = self.reader.take() else {
            return false;
        };
        self.home_date = page.date;
        self.home_stale = false;
        if let screens::Screen::Home(home) = &mut self.home {
            home.apply(page);
        }
        true
    }

    // The browser is on the screen again, whether the film played
    // through or the `Play` never started, so the page comes back.
    fn presented(&mut self) {
        if let Some(state) = &mut self.loading {
            state.leave(self.clock);
        }
    }

    // The shade lifted, so the home page is read again where it is
    // behind: a change the source reported while the shade was down, or a
    // shade that lifts on a new day, whose draw is another day's. The home
    // page is read whether or not a screen covers it, because back pops to
    // it with no draw of its own.
    fn lifted(&mut self) {
        self.refresh_home();
    }

    // Resolve the choice through the catalog and publish it. The browser
    // resolves the list because it holds the catalog, and the operator
    // creates the `Play` because it holds the credential. A choice with
    // no main file starts nothing, and the line in the pod log is the
    // only sign of the gap.
    //
    // The answer is whether the catalog resolved a film, and not whether
    // the request went out. A run with no bus browses the same way, and
    // the page it draws while it waits is the same page.
    fn request_play(&mut self, library: &str, selection: &Selection) -> bool {
        let items = self.source.play(library, selection);
        if items.is_empty() {
            eprintln!(
                "media-browser: no file to play for {} in {library}",
                selection.named()
            );
            return false;
        }
        let Some(bus) = &self.bus else {
            return true;
        };
        // An older library operator names no topic, and the browser
        // then browses and starts nothing. The line in the pod log is
        // the only sign of the gap.
        if self.play_topic.is_empty() {
            eprintln!("media-browser: no play topic, so this browser starts nothing");
            return true;
        }
        // A request is an event, so it is not retained: a broker that
        // held the last one would replay it to the operator on every
        // reconnect.
        bus.publish(&self.play_topic, play::payload(library, &items), false);
        true
    }

    // The strip the browser draws over whatever screen is on the stack,
    // or nothing while the shade is down. No screen draws it, so every
    // screen carries it in the same place. The field is the search
    // wall's own, read off the top screen.
    fn strip(&self) -> Option<views::clock::strip::Strip<'_>> {
        (!self.asleep).then(|| views::clock::strip::Strip {
            time: self.time,
            field: self.top().field(),
            focused: self.on_strip,
        })
    }

    // One press while the strip holds focus. Select opens the search
    // wall with the grid, or shows the grid on a search wall. Down gives
    // focus back to the screen. A word that edits the field types into
    // it on a search wall and gives focus back with it. Every other
    // word, the arrows included, moves nothing.
    fn on_strip(&mut self, name: &str) -> bool {
        match name {
            "enter" => {
                match self.top().searching() {
                    true => {
                        self.on_strip = false;
                        let top = self.stack.last_mut().unwrap_or(&mut self.home);
                        top.show_grid();
                    }
                    false => self.search("", true),
                }
                true
            }
            "down" => {
                self.on_strip = false;
                true
            }
            _ if views::field::edits(name) && self.top().searching() => {
                self.on_strip = false;
                self.on_screen(name)
            }
            _ => false,
        }
    }

    // One press the screen on top takes. An up the screen answers
    // `Still` to moved nothing there, so it puts focus on the strip.
    fn on_screen(&mut self, name: &str) -> bool {
        let top = self.stack.last_mut().unwrap_or(&mut self.home);
        let step = top.key(name, &mut self.source);
        let still = matches!(step, Step::Still);
        self.take(step);
        if still && name == "up" {
            self.on_strip = true;
            return true;
        }
        !still
    }
}

impl<S: Source, A: Art> Screen for Browser<S, A> {
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

    fn key(&mut self, name: &str) -> bool {
        // A press during the loading state reaches no screen under it.
        // Back exits the state here and now, and cancels nothing: the
        // `Play` this browser asked for is the operator's to run.
        if self.loading.is_some() {
            if name == "escape" || name == "backspace" {
                self.presented();
            }
            return true;
        }
        let mut changed = true;
        match name {
            // Escape on the strip gives focus back to the screen and pops
            // nothing, because the strip is over the stack and not on it.
            "escape" if self.on_strip => self.on_strip = false,
            // The screen on top is asked first, because a search wall
            // reads backspace as a deleted character, and escape as the
            // grid closed or the text cleared. Every other screen takes
            // neither, and both words are then back.
            "escape" | "backspace" => {
                let top = self.stack.last_mut().unwrap_or(&mut self.home);
                match top.escape(name, &mut self.source) {
                    Some(step) => self.take(step),
                    None => self.back(),
                }
            }
            "home" => self.home(),
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

    // Art that landed changes the frame and not the rows, so a
    // delivery redraws what is already read and only a changed source
    // re-reads the screen. A home page the reader answered lands here too,
    // and it is a frame to draw.
    fn pump(&mut self, at: f64) -> bool {
        // The clock moves here as well as on a frame, because a covered
        // browser draws none: a wake that arrived under a film would
        // otherwise start the exit at the second of the last frame before
        // the film, which is already spent.
        self.clock = at;
        let folded = self.drain_bus();
        let delivered = self.store.get_mut().delivered();
        let landed = self.landed_home();
        if !self.source.changed() {
            return folded || delivered || landed;
        }
        // A change marks the home page behind whether or not a page covers
        // it, because back pops to the home page with no read of its own.
        self.home_stale = true;
        self.reread_top();
        true
    }

    // The source, the art store, the home page's reader, and the bus
    // deliver on threads of their own, so all four take the handle that
    // wakes the loop.
    fn wake_by(&mut self, wake: Waker) {
        self.source.wake_by(wake.clone());
        if let Some(bus) = &self.bus {
            bus.wake_on_delivery(wake.clone());
        }
        self.reader.wake_by(wake.clone());
        self.store.get_mut().wake_by(wake);
    }

    fn surface_due(&mut self) -> bool {
        std::mem::take(&mut self.surface_due)
    }

    fn art_counts(&self) -> ArtCounts {
        self.store.borrow().counts()
    }

    // The size of the source's search index, for the stats line. A
    // source with no index answers nothing, and the line leaves the
    // numbers out.
    fn index_size(&mut self) -> Option<Size> {
        self.source.index_size()
    }

    // The clock is read here alone, so the rest is measured on the same
    // clock the harness drives every frame with.
    fn tick(&mut self, at: f64) {
        self.clock = at;
        self.time = clock::now();
        self.minute = Some(at + clock::seconds_to_next_minute());
        if self.loading.is_some_and(|state| state.done(at)) {
            self.loading = None;
        }
        if self.rest.is_some_and(|due| at >= due) {
            self.rest = None;
            self.prefetch();
        }
    }

    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer> {
        // The shade is down, so the frame is the clear color and nothing over
        // it. The screen and its focus are held for the wake.
        let Some(strip) = self.strip() else {
            return Space::new().width(Length::Fill).height(Length::Fill).into();
        };

        let screen = self.top().view(
            &self.store,
            self.loading.map(|state| state.curtain(self.clock)),
            !self.on_strip,
        );

        // The strip and the row are the browser's own layers over
        // whatever screen is on the stack, so a page change under them
        // neither resets them nor covers them.
        let mut layers = vec![
            screen,
            canvas(strip)
                .width(Length::Fill)
                .height(Length::Fill)
                .into(),
        ];
        if let Some(row) = self.level.row(self.clock) {
            layers.push(canvas(row).width(Length::Fill).height(Length::Fill).into());
        }
        Stack::with_children(layers)
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }

    // Every view here is still until something changes it, and the
    // source wakes the loop itself, so an idle browser schedules nothing
    // and the loop waits on events.
    //
    // Four things schedule a frame: the end of a rest; every frame of the
    // loading state, which answers now on every ask so the mark pulses at
    // the loop's own floor rate; the volume row, which asks for a frame
    // through each of its fades and names the second it starts to leave
    // through the hold between them; and the clock, which asks for the
    // second the minute turns. Nothing under a film schedules a frame,
    // because those frames would draw a black shade nobody sees.
    fn next_frame(&self, at: f64) -> Option<f64> {
        let drawing = !self.asleep;
        let loading = (drawing && self.loading.is_some()).then_some(at);
        let level = drawing.then(|| self.level.next_frame(at)).flatten();
        let minute = drawing.then_some(self.minute).flatten();
        [loading, level, minute, self.rest]
            .into_iter()
            .flatten()
            .min_by(f64::total_cmp)
    }
}

#[cfg(test)]
mod tests;
