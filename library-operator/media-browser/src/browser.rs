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

use media_screen::status::Activity;
use media_screen::{Bus, Moment};

use crate::art::{Art, ArtCounts};
use crate::audience::{Audience, Person};
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
    // The people in the room and the second of the last press. Every play
    // request is recorded against them, and every progress read is for
    // them.
    audience: Audience,
    // Where the `Person` list is read from again each time the picker
    // opens, or nothing on a run that named no file.
    people_file: Option<std::path::PathBuf>,
    // Whether the shade is down. The browser never decides it: it asks for
    // the shade, the crate decides, and the moment comes back here.
    asleep: bool,
    // Whether a film covers the surface. The bus says so in every status
    // whose activity is playing, and the harness builds no frame while it
    // stands. It lifts on the status that returns to idle, on the wake,
    // and on the present.
    covered: bool,
    // Whether a present asked for a fresh Wayland surface.
    surface_due: bool,
    // Whether the return waits for that surface. The return runs on the
    // clock, and the compositor takes its own time to map a fresh
    // window, so a return started at the present would run out before
    // the first frame anyone sees. It starts on the frame the harness
    // reports the surface up.
    returning: bool,
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
    // The picker over the stack while the browser has no answer to who is
    // watching, and nothing once it has one.
    picker: Option<screens::audience::Picker>,
    // The volume row's state, which the level moments the bus delivers fold
    // into.
    level: volume::Level,
    // The reading the strip draws, read at every tick.
    time: clock::Time,
    // Whether the strip over every screen holds focus. It is the
    // browser's field and not a screen's, because the strip is the
    // browser's layer, and it clears whenever the stack changes.
    on_strip: bool,
    // Which of the strip's targets holds focus while the strip holds it:
    // the glass, or the circles of the room.
    strip_focus: views::clock::strip::Target,
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
        let home = screens::Screen::Home(home::Home::open(&mut source, &[]));
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
            audience: Audience::default(),
            people_file: None,
            asleep: false,
            covered: false,
            surface_due: false,
            returning: false,
            page: PAGE,
            clock: 0.0,
            rest: None,
            loading: None,
            picker: None,
            level: volume::Level::default(),
            time: clock::now(),
            on_strip: false,
            strip_focus: views::clock::strip::Target::default(),
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

    /// The browser over this `Person` list, with the audience the run named
    /// already answered. An empty list is a cluster with no `Person`, and
    /// the browser then asks nobody and records nobody.
    pub fn with_audience(mut self, people: Vec<Person>, preset: Vec<String>) -> Self {
        self.audience = Audience::new(people);
        if !preset.is_empty() {
            self.audience.answer(preset, 0.0);
        }
        // The home page opened before the audience was known, so its
        // continue-watching row is behind, and the loop's first pass reads
        // the page again.
        self.home_stale = true;
        self
    }

    /// Read the `Person` list again from this file each time the picker
    /// opens.
    pub fn with_people_file(mut self, path: Option<std::path::PathBuf>) -> Self {
        self.people_file = path;
        self
    }

    /// The people in the room, which the picker draws and a play request is
    /// recorded against.
    pub fn audience(&self) -> &Audience {
        &self.audience
    }

    // Read the `Person` list from its file again, so a picker opened after
    // a `Person` was added draws them. A file that cannot be read leaves
    // the list the browser holds, because a list that was good at the
    // start is better than none.
    fn learn_people(&mut self) {
        let Some(path) = &self.people_file else {
            return;
        };
        let people = std::fs::read(path)
            .ok()
            .and_then(|bytes| crate::audience::people_from_json(&bytes).ok());
        match people {
            Some(people) => self.audience.learn(people),
            None => eprintln!(
                "media-browser: the people file could not be read: {}",
                path.display()
            ),
        }
    }

    /// Whether the shade is down. The frame is black while it is.
    pub fn asleep(&self) -> bool {
        self.asleep
    }

    // Raise the picker where the browser has no answer to who is watching.
    // The answer is whether it went up, which is a frame to draw. A
    // browser that knows no people never asks. Neither does one under the
    // shade or under the loading state, because nobody sees a picker drawn
    // there.
    fn ask(&mut self) -> bool {
        let due = self.picker.is_none()
            && !self.asleep
            && self.loading.is_none()
            && self.audience.needs_answer(self.clock);
        if due {
            self.learn_people();
            self.picker = Some(screens::audience::Picker::open(
                self.audience.known().len(),
                &[],
            ));
        }
        due
    }

    // Take the picker's answer: set who is watching, drop the gate, and
    // read again for the new people. The home page goes through its own
    // reader, and the screen on top reads its progress here, because both
    // were read for whoever was watching before.
    fn answered(&mut self, chosen: Vec<usize>) {
        let names = chosen
            .into_iter()
            .filter_map(|index| self.audience.known().get(index))
            .map(|person| person.name.clone())
            .collect();
        self.audience.answer(names, self.clock);
        self.picker = None;
        self.home_stale = true;
        let people = self.audience.current(self.clock).to_vec();
        if let Some(top) = self.stack.last_mut() {
            top.read_progress(&mut self.source, &people);
        }
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
                self.covered = false;
                self.presented();
                self.lifted();
            }
            Moment::Present => {
                self.surface_due = true;
                self.covered = false;
                self.returning = true;
                self.lifted();
            }
            // Starting leaves the page and its pulse on the screen; the
            // film covers it once it plays.
            Moment::Status(status) => self.covered = status.activity == Activity::Playing,
            // A level brings up the volume row, which draws over every
            // screen.
            Moment::Level { volume, pressed } => self.level.fold(volume, pressed, self.clock),
            // The mark stands between the equipment that owns the room's
            // level and this client's row.
            Moment::Owner(mark) => self.level.own(&mark),
            // The browser draws no identity block, so focus changes nothing
            // here.
            Moment::Focus { .. } => {}
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
    // The answer is whether a page landed, which is a frame to draw.
    fn refresh_home(&mut self) -> bool {
        let today = (self.today)();
        if !self.home_stale && self.home_date == today {
            return false;
        }
        let people = self.audience.current(self.clock).to_vec();
        self.reader.ask(&mut self.source, today, people);
        self.landed_home()
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
    fn request_play(&mut self, library: &str, selection: &Selection, start: Option<i64>) -> bool {
        let items = self.source.play(library, selection);
        if items.is_empty() {
            eprintln!(
                "media-browser: no file to play for {} in {library}",
                selection.named()
            );
            return false;
        }
        // The work is read before the bus is borrowed, because the read
        // wants the source and the publish holds a borrow of the bus.
        let identity = self.source.identity(library, selection);
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
        bus.publish(
            &self.play_topic,
            play::payload(
                library,
                &items,
                self.audience.current(self.clock),
                &identity,
                start,
            ),
            false,
        );
        true
    }

    // The strip the browser draws over whatever screen is on the stack,
    // or nothing while the shade is down. No screen draws it, so every
    // screen carries it in the same place. The field is the search
    // wall's own, read off the top screen.
    fn strip(&self) -> Option<views::clock::strip::Strip<'_>> {
        // Focus falls back to the glass where no circles are drawn, so the
        // mark never goes around nothing.
        let focus = match self.circles() {
            true => self.strip_focus,
            false => views::clock::strip::Target::Glass,
        };
        (!self.asleep).then(|| views::clock::strip::Strip {
            time: self.time,
            field: self.top().field(),
            focus: self.on_strip.then_some(focus),
            letters: self.audience.letters(self.clock),
        })
    }

    // Whether the strip draws the circles of the room: where somebody is
    // watching and no search field stands over the same band.
    fn circles(&self) -> bool {
        !self.audience.current(self.clock).is_empty() && self.top().field().is_none()
    }

    // Put the browser's focus on the strip, at the glass, which is where a
    // screen hands focus up to.
    fn enter_strip(&mut self) {
        self.on_strip = true;
        self.strip_focus = views::clock::strip::Target::Glass;
    }

    // Give focus back to the screen, and leave the strip at the glass for
    // the next press that reaches it.
    fn leave_strip(&mut self) {
        self.on_strip = false;
        self.strip_focus = views::clock::strip::Target::Glass;
    }

    // One press while the strip holds focus. Left from the glass reaches
    // the circles of the room, where there are any, and right returns to
    // the glass. Select on the circles asks who is watching again, with the
    // room already chosen. Select on the glass opens the search wall with
    // the grid, or shows the grid on a search wall. Down gives focus back
    // to the screen. A word that edits the field types into it on a search
    // wall and gives focus back with it. Every other word moves nothing.
    fn on_strip(&mut self, name: &str) -> bool {
        use views::clock::strip::Target;
        match (name, self.strip_focus) {
            ("left", Target::Glass) if self.circles() => {
                self.strip_focus = Target::Circles;
                true
            }
            ("right", Target::Circles) => {
                self.strip_focus = Target::Glass;
                true
            }
            ("enter", Target::Circles) => {
                self.learn_people();
                self.picker = Some(screens::audience::Picker::open(
                    self.audience.known().len(),
                    &self.audience.chosen(self.clock),
                ));
                true
            }
            ("enter", _) => {
                match self.top().searching() {
                    true => {
                        self.leave_strip();
                        let top = self.stack.last_mut().unwrap_or(&mut self.home);
                        top.show_grid();
                    }
                    false => self.search("", true),
                }
                true
            }
            ("down", _) => {
                self.leave_strip();
                true
            }
            _ if views::field::edits(name) && self.top().searching() => {
                self.leave_strip();
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
            self.enter_strip();
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
        // Every press holds the audience's answer open, whatever the press
        // then does, because a person at the remote is a person in the room.
        self.audience.touch(self.clock);
        // A press during the loading state reaches no screen under it.
        // Back exits the state here and now, and cancels nothing: the
        // `Play` this browser asked for is the operator's to run.
        if self.loading.is_some() {
            if name == "escape" || name == "backspace" {
                self.presented();
            }
            return true;
        }
        // The picker takes every press while it stands, and the press that
        // raises it does nothing else, so a room that answered hours ago
        // never plays under the last room's name.
        if self.ask() {
            return true;
        }
        if let Some(picker) = &mut self.picker {
            if let Some(chosen) = picker.key(name) {
                self.answered(chosen);
            }
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
                    Some(step) => self.take(step),
                    None => self.back(),
                }
            }
            "home" => self.home(),
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
        if self.source.changed() {
            // A change marks the home page behind whether or not a page
            // covers it, because back pops to the home page with no read of
            // its own.
            self.home_stale = true;
            self.reread_top();
            return true;
        }
        // A home page behind the audience it was read for is asked for
        // here, so a run that names an audience and takes no press still
        // draws the continue-watching row. A read already in flight is left
        // to land, because the ask would otherwise repeat on every pass of
        // the loop.
        let refreshed = !self.reader.reading() && self.refresh_home();
        let asked = self.ask();
        folded || delivered || landed || refreshed || asked
    }

    // The source, the art store, the home page's reader, and the bus
    // deliver on threads of their own, so all four take the handle that
    // wakes the loop.
    // The fresh surface is up, so the return the present held starts now
    // and its first frame is the first one on the new window.
    fn surfaced(&mut self, at: f64) {
        if !self.returning {
            return;
        }
        self.returning = false;
        self.clock = at;
        self.presented();
    }

    // The page's backdrop is decoded at the logical size of the window,
    // and the store scales every ask to the panel and bounds its memory
    // by the panel's size.
    fn scaled(&mut self, logical: (u32, u32), scale: f32) {
        self.page = logical;
        let physical = (
            (logical.0 as f32 * scale).round() as u32,
            (logical.1 as f32 * scale).round() as u32,
        );
        self.store.get_mut().scaled(physical, scale);
    }

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
        self.ask();
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
        if let Some(picker) = &self.picker {
            layers.push(
                canvas(screens::audience::Layer {
                    people: self.audience.known(),
                    picker,
                })
                .width(Length::Fill)
                .height(Length::Fill)
                .into(),
            );
        }
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
    fn covered(&self) -> bool {
        self.covered
    }

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
