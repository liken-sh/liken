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
use crate::audience::{self, Audience, Person};
use crate::bus::{self, next, play};
use crate::catalog::draw::Date;
use crate::catalog::search::Size;
use crate::catalog::{Selection, Source};
use crate::clock;
use crate::harness::{Screen, Waker};
use crate::look;
use crate::screens::upnext::{self, Next};
use crate::screens::{self, Step, home, lights, loading, volume};
use crate::views;

mod keys;
mod reader;
// The refresh policy: the one place that decides when the browser reads
// the catalog again.
mod refresh;
// The stack module: the screens a person descended through, and every
// move across them.
mod stack;

use keys::key_of;
use refresh::Refresh;

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
    // The topic the browser keeps who is watching on, retained. It is
    // this browser's own message, written for the browser that starts
    // after this one, so the browser is both the writer and the reader.
    audience_topic: String,
    // The wall clock, in whole seconds since the Unix epoch. It is a
    // field so a test states the second every stamp carries. The run
    // clock cannot carry the stamp: it starts at zero on every run, and
    // the message must outlive the process.
    now: fn() -> i64,
    // The people in the room and the second of the last press. Every play
    // request is recorded against them, and every progress read is for
    // them.
    audience: Audience,
    // Where the `Person` list is read from again each time the picker
    // opens, or nothing on a run that named no file.
    people_file: Option<std::path::PathBuf>,
    // The refresh policy. It holds the shade and the cover, and the
    // browser decides neither: it asks for the shade, the crate decides,
    // and the moment comes back here; the bus says in every status
    // whether a film covers the surface. It also holds every change that
    // waits to be read, and it alone says when a read runs.
    refresh: Refresh,
    // What the unit was doing in the last status. The browser holds it
    // because the return runs on the move to `Idle` and not on the word
    // `Idle` itself: the operator publishes a status on every change of
    // the unit, and the browser would otherwise return the page again
    // on each one.
    activity: Activity,
    // Whether the page's return waits for a frame. A film covers the
    // browser and the shade stops the loop, so the second a film ends
    // is not always a second a frame follows, and a return started
    // there would be spent before anyone saw a frame of it. The next
    // tick starts it, and every tick is a frame's.
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
    // How far down the lights are, or nothing while the room is at full.
    // The state runs beside the loading state, because the whole frame
    // dims and the curtain's logo does not.
    lights: Option<lights::Lights>,
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
        let home = screens::Screen::Home(home::Home::open(&mut source, &[], &[]));
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
            audience_topic: String::new(),
            now: clock::seconds,
            audience: Audience::default(),
            people_file: None,
            refresh: Refresh::default(),
            activity: Activity::Idle,
            returning: false,
            page: PAGE,
            clock: 0.0,
            rest: None,
            loading: None,
            lights: None,
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

    /// The browser on a bus, the topic it publishes a play request on,
    /// and the topic it keeps who is watching on. A `Player` whose status
    /// names no bus wires none, and the browser then takes the keyboard
    /// alone.
    pub fn with_bus(
        mut self,
        bus: Option<Box<dyn Bus>>,
        play_topic: String,
        audience_topic: String,
    ) -> Self {
        self.bus = bus;
        self.play_topic = play_topic;
        self.audience_topic = audience_topic;
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
        self.refresh.asleep()
    }

    // The picker goes up as a layer over the stack and pops nothing, so
    // the screen under it is the one that comes back when the answer is
    // taken. The list is read again first, and the people of the current
    // answer start chosen. An audience that needs an answer holds none,
    // so a picker raised by the ask starts with nobody.
    fn raise_picker(&mut self) {
        self.learn_people();
        let chosen = self.audience.chosen(self.clock);
        self.picker = Some(screens::audience::Picker::open(
            self.audience.known().len(),
            &chosen,
        ));
    }

    // Raise the picker where the browser has no answer to who is watching.
    // The answer is whether it went up, which is a frame to draw. A
    // browser that knows no people never asks. Neither does one under the
    // shade or under the loading state, because nobody sees a picker drawn
    // there.
    fn ask(&mut self) -> bool {
        // The idle window ends an answer whether or not anybody pressed, so
        // the message on the bus goes here as well as on a press.
        if self.audience.lapse(self.clock) {
            self.clear_audience();
        }
        let due = self.picker.is_none()
            && !self.asleep()
            && self.loading.is_none()
            && self.audience.needs_answer(self.clock);
        if due {
            self.raise_picker();
        }
        due
    }

    // Take the picker's answer: set who is watching, drop the gate, put
    // the room on the bus, and read again for the new people.
    fn answered(&mut self, chosen: Vec<usize>) {
        let names = chosen
            .into_iter()
            .filter_map(|index| self.audience.known().get(index))
            .map(|person| person.name.clone())
            .collect();
        self.audience.answer(names, self.clock);
        self.picker = None;
        self.publish_audience((self.now)());
        self.read_again();
    }

    // Read again for the people who are watching now. The home page goes
    // through its own reader, and the screen on top reads its progress
    // here, because both were read for whoever was watching before.
    fn read_again(&mut self) {
        self.home_stale = true;
        let people = self.audience.current(self.clock).to_vec();
        if let Some(top) = self.stack.last_mut() {
            top.read_progress(&mut self.source, &people);
        }
    }

    // Put the answer on the bus, retained, under this wall second. The
    // browser that starts after this one reads it back, so a pod that
    // restarts inside the idle window draws the room it had and asks
    // nobody. A
    // run with no bus, and one the operator named no topic for, keeps the
    // answer to itself and behaves the same in every other way.
    //
    // An audience with no answer publishes nothing: the message that
    // clears the topic is the lapse's, and it says something else.
    fn publish_audience(&mut self, at: i64) {
        let Some(people) = self.audience.watching(self.clock) else {
            return;
        };
        let Some(bus) = &self.bus else {
            return;
        };
        if self.audience_topic.is_empty() {
            return;
        }
        bus.publish(
            &self.audience_topic,
            bus::audience::payload(&people, at),
            true,
        );
        self.audience.stamped(at);
    }

    // The answer lapsed, so the message goes with it. An empty payload is
    // how MQTT drops a retained message, and without the clear a browser
    // that starts next would read a room that went home hours ago.
    fn clear_audience(&self) {
        let Some(bus) = &self.bus else {
            return;
        };
        if self.audience_topic.is_empty() {
            return;
        }
        bus.publish(&self.audience_topic, Vec::new(), true);
    }

    // The bus session started, so this client republishes the retained
    // state it owns, which is the rule every program on this broker
    // follows. A reconnect is not a press, so the answer keeps the stamp
    // it carries and ages the way it would have. An answer that never
    // reached the bus, which is the one a run named on the command line,
    // goes out under the second of the connection.
    fn republish_audience(&mut self) {
        let at = match self.audience.stamp() {
            0 => (self.now)(),
            stamp => stamp,
        };
        self.publish_audience(at);
    }

    // Take the room off the bus: the message this browser wrote before it
    // restarted, which the broker kept and delivered on the subscription.
    // The browser takes it only where it holds no answer of its own, so a
    // run that named an audience, and one that has already asked, both
    // keep the newer answer.
    //
    // A stamp further back than the idle window is a room that went home,
    // and the browser then asks as it would have with no message at all.
    fn restore(&mut self, payload: &[u8]) {
        if self.audience.watching(self.clock).is_some() {
            return;
        }
        let Some(held) = bus::audience::held(payload) else {
            return;
        };
        let age = ((self.now)() - held.at) as f64;
        if age > audience::IDLE_SECONDS {
            return;
        }
        // The run clock starts at zero, so the stamp comes back onto it as
        // the distance from now, which is a second before zero on a run
        // that has just opened. The lapse measures that distance the same
        // way either side of zero.
        let names = held.people.into_iter().map(|person| person.name).collect();
        self.audience.answer(names, self.clock - age);
        self.audience.stamped(held.at);
        // A picker the ask raised stands over the screen with nobody part
        // way through an answer, so the room that came back closes it. One
        // somebody is answering stands: their answer is the newer one.
        if self
            .picker
            .as_ref()
            .is_some_and(screens::audience::Picker::untouched)
        {
            self.picker = None;
        }
        self.read_again();
    }

    // One press against the audience. It holds a standing answer open, and
    // it moves the stamp on the bus at most once a minute, so a walk
    // across a wall publishes one message and not one per key. A press
    // that finds a lapsed answer clears the message instead: the room went
    // home, and the browser asks who is here now.
    fn pressed(&mut self) {
        if self.audience.touch(self.clock) {
            self.clear_audience();
            return;
        }
        let now = (self.now)();
        if self.audience.stamp_due(now) {
            self.publish_audience(now);
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
            Moment::Sleep => self.refresh.shade(true),
            Moment::Wake => {
                self.refresh.shade(false);
                self.refresh.cover(false);
                self.presented();
                self.lifted();
            }
            // The status is the whole word the browser has on whether a
            // film is over its surface. Starting leaves the page and its
            // pulse on the screen; the film covers it once it plays. The
            // move to `Idle` is the end of the work the page asked for,
            // whether the film played through or the `Play` never
            // played, so the page comes back where it was with its
            // return and a read of what changed under the film. The
            // compositor shows this window again the moment the film's
            // surface goes, so the return needs no window of its own.
            //
            // The move away from `Idle` is the one way the lights go
            // down, because a film is on its way to this screen whether
            // or not this browser asked for it: a `Play` that kubectl,
            // another client, or an automation created dims the page the
            // same way and lifts it at the end. The curtain is the
            // select's and the lights are the status's, and the two
            // states run beside each other, so a select whose `Play` is
            // still on its way draws its curtain over a page at full
            // brightness.
            Moment::Status(status) => {
                if self.activity != Activity::Idle && status.activity == Activity::Idle {
                    self.returning = true;
                    self.lifted();
                }
                if self.activity == Activity::Idle && status.activity != Activity::Idle {
                    self.lights = Some(lights::Lights::entered(self.clock));
                }
                self.activity = status.activity;
                self.refresh.cover(status.activity == Activity::Playing);
            }
            // A level brings up the volume row, which draws over every
            // screen.
            Moment::Level { volume, pressed } => self.level.fold(volume, pressed, self.clock),
            // The mark stands between the equipment that owns the room's
            // level and this client's row.
            Moment::Owner(mark) => self.level.own(&mark),
            // The browser draws no identity block, so focus changes nothing
            // here.
            Moment::Focus { .. } => {}
            // A person took the offer the display drew. The bytes are the
            // request this browser wrote onto that Play.
            Moment::PlayNext(request) => self.play_next(&request),
            // The room, as the broker kept it for this screen. A retained
            // delivery is the catch-up on the subscription, which is the
            // message the browser before this one left. A live delivery is
            // the echo of this browser's own publish, and it says nothing
            // this browser does not already hold.
            Moment::Message {
                topic,
                payload,
                retained,
            } => {
                if retained && topic == self.audience_topic {
                    self.restore(&payload);
                }
            }
            Moment::Connected => self.republish_audience(),
        }
    }

    // Start what follows the film that is playing. The block is this
    // browser's own words, so it reads the resume position and the next
    // offer the way the page that wrote the first request read them, and
    // the run chains. A block this browser did not write starts nothing.
    fn play_next(&mut self, payload: &[u8]) {
        let Some(request) = next::request(payload) else {
            eprintln!("media-browser: the play-next ask carried no request of this browser's");
            return;
        };
        let people = self.audience.current(self.clock).to_vec();
        let (start, next) = upnext::again(&mut self.source, &request, &people);
        self.take(Step::Play {
            library: request.library,
            selection: request.selection,
            start,
            next,
        });
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
        let letters = self.audience.letters(self.clock);
        self.reader.ask(&mut self.source, today, people, letters);
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
    // through or the `Play` never started, so the page comes back and the
    // lights come up with it.
    fn presented(&mut self) {
        if let Some(state) = &mut self.loading {
            state.leave(self.clock);
        }
        if let Some(lights) = &mut self.lights {
            lights.lift(self.clock);
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
    fn request_play(
        &mut self,
        library: &str,
        selection: &Selection,
        start: Option<i64>,
        next: Option<&Next>,
    ) -> bool {
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
                next,
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
        (!self.asleep()).then(|| views::clock::strip::Strip {
            time: self.time,
            field: self.top().field(),
            focus: self.on_strip.then_some(focus),
            letters: match self.nobody_watching() {
                true => vec!["?".to_string()],
                false => self.audience.letters(self.clock),
            },
            asking: self.nobody_watching(),
        })
    }

    // Whether the strip draws the circles of the room: where somebody is
    // watching, or nobody is and the "?" circle stands for them, and no
    // search field stands over the same band.
    fn circles(&self) -> bool {
        (self.nobody_watching() || !self.audience.current(self.clock).is_empty())
            && self.top().field().is_none()
    }

    // Whether the strip draws the one "?" circle in place of the room:
    // the browser knows people, nobody is watching (an answer of nobody,
    // or a lapsed one), and the picker is not up. It is the way back to
    // the picker for a person who answered nobody. A browser that knows
    // no people never asks and would raise an empty picker, so it draws
    // none, the way the people key moves nothing there. Under the picker
    // the strip draws no circles.
    fn nobody_watching(&self) -> bool {
        self.picker.is_none()
            && !self.audience.known().is_empty()
            && self.audience.current(self.clock).is_empty()
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
    // room already chosen, and with nobody chosen from the "?" circle. Select on the glass opens the search wall with
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
                self.raise_picker();
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
        if self.asleep() {
            return Color::BLACK;
        }
        look::BACKGROUND
    }

    fn key(&mut self, name: &str) -> bool {
        // Every press holds the audience's answer open, whatever the press
        // then does, because a person at the remote is a person in the room.
        self.pressed();
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
            // The people key raises the picker over whatever screen is
            // up, the way home pops to the home page. A press that arrives
            // while the picker stands never reaches here: the picker took
            // it above and binds no word for people, so it stands as it
            // was. A browser that knows no people has an empty picker to
            // draw and nothing to ask, so the key moves nothing there, the
            // way the ask never raises one.
            "people" => match self.audience.known().is_empty() {
                true => changed = false,
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
        // The source names what changed and the policy decides the read.
        // Nothing else here decides one, so a film's progress rows, which
        // arrive once a second for two hours, cannot order a full read of
        // the screen once a second.
        let change = self.source.changed();
        self.refresh.changed(change, at);
        if self.refresh.due(at) {
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
        // The policy gates this ask too. It is the second way a read
        // starts, and a hidden screen must start neither.
        let refreshed = self.refresh.shown() && !self.reader.reading() && self.refresh_home();
        let asked = self.ask();
        folded || delivered || landed || refreshed || asked
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
        // The return the film's end marked starts here, on the clock
        // of the frame this tick belongs to, so its whole length runs
        // on frames a person sees.
        if std::mem::take(&mut self.returning) {
            self.presented();
        }
        self.ask();
        self.time = clock::now();
        self.minute = Some(at + clock::seconds_to_next_minute());
        if self.loading.is_some_and(|state| state.done(at)) {
            self.loading = None;
        }
        if self.lights.is_some_and(|state| state.done(at)) {
            self.lights = None;
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

        let curtain = self.loading.map(|state| state.curtain(self.clock));
        let screen = self.top().view(&self.store, curtain, !self.on_strip);

        // The dim of the room is the frame's layer and not a page's, so
        // the home page, a wall, and a title's page all go down the same
        // way: a `Play` the browser did not start arrives while any of
        // them is showing. The dim covers the screen and the art the
        // curtain drew again over it, and the curtain's own front draws
        // over the dim, so the room goes down while the logo keeps the
        // brightness it pulses at.
        let mut layers = vec![screen];
        let level = self.lights.map_or(1.0, |state| state.level(self.clock));
        if level < 1.0 {
            layers.push(views::layers::dim(level));
        }
        if let Some(front) = curtain.and_then(|curtain| self.top().front(&self.store, curtain)) {
            layers.push(front);
        }

        // The strip and the row are the browser's own layers over
        // whatever screen is on the stack, so a page change under them
        // neither resets them nor covers them.
        layers.push(
            canvas(strip)
                .width(Length::Fill)
                .height(Length::Fill)
                .into(),
        );
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
    // Four things schedule a frame: the end of a rest; every frame of
    // the loading state and of the lights beside it, which answer now on
    // every ask so the mark pulses and the room dims at the loop's own
    // floor rate; the volume row, which asks for a frame through each of
    // its fades and names the second it starts to leave through the hold
    // between them; and the clock, which asks for the second the minute
    // turns. A fifth wakes the loop with no frame: the
    // second a held read comes due. Nothing under a film schedules a
    // frame, because those frames would draw a black shade nobody sees.
    fn covered(&self) -> bool {
        self.refresh.covered()
    }

    fn next_frame(&self, at: f64) -> Option<f64> {
        let drawing = !self.refresh.asleep();
        let loading = (drawing && self.loading.is_some()).then_some(at);
        let lights = (drawing && self.lights.is_some()).then_some(at);
        let level = drawing.then(|| self.level.next_frame(at)).flatten();
        let minute = drawing.then_some(self.minute).flatten();
        let read = self.refresh.next_due();
        [loading, lights, level, minute, self.rest, read]
            .into_iter()
            .flatten()
            .min_by(f64::total_cmp)
    }
}

#[cfg(test)]
mod tests;
