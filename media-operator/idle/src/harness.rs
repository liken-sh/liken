//! The idle screen's harness: the flags, the frame loop, and the
//! measurements.
//!
//! A screen here is a piece of state with a clock and a view. The harness owns
//! everything around it: the winit window, the wgpu surface, the iced renderer,
//! the scripted key timeline, the frame capture, and the statistics file. The
//! last two are the `measure` feature, and the image builds without it.
//!
//! The harness drives its own winit loop instead of calling `iced::application`
//! because it must reach the renderer directly. A frame capture and a frame
//! clock are not part of the high-level entry point.

pub mod frame;
pub mod graphics;
pub mod options;
pub mod timeline;
pub mod watchdog;

// The capture and the measurements are the `measure` feature. Nothing a pod
// runs reaches either one, because the operator sets no command on the idle
// container and the image's entrypoint takes no flag, so the image builds
// without them.
#[cfg(feature = "measure")]
pub mod capture;
#[cfg(feature = "measure")]
pub mod stats;

use iced_wgpu::Renderer;
use iced_wgpu::graphics::Viewport;
use iced_winit::core::{Color, Element, Event, Size, Theme};
use iced_winit::runtime::user_interface;
use iced_winit::winit;
use iced_winit::{Clipboard, conversion};

use winit::event::WindowEvent;
use winit::event_loop::{ControlFlow, EventLoop};
use winit::keyboard::{Key, ModifiersState, NamedKey};

#[cfg(feature = "measure")]
use capture::Captures;
use graphics::Graphics;
pub use options::{Invocation, Options};
#[cfg(feature = "measure")]
use stats::Stats;
use timeline::Timeline;
use watchdog::Watchdog;

/// The key that ends a run, from the keyboard or from a script.
pub const QUIT: &str = "q";

/// What the harness needs from a screen. The harness advances the clock, hands
/// over each key the script or the keyboard produced, and asks for a view.
pub trait Screen {
    /// The messages the screen's own widgets emit.
    type Message: std::fmt::Debug + Send + 'static;

    /// The color behind everything. It is the clear color of the frame, so it
    /// also fills a capture.
    fn background(&self) -> Color {
        Color::BLACK
    }

    /// One key press, named the way the script names it: a single letter or
    /// digit, or `up`, `down`, `left`, `right`.
    fn key(&mut self, name: &str);

    /// Fold in what the screen's own sources delivered since the last call,
    /// at `at` seconds on the clock. The answer is whether anything folded,
    /// so the harness drops a stale schedule and asks the screen again.
    ///
    /// The harness calls this on every wake of the loop, not only on a frame.
    /// A covered Wayland surface receives no frame callbacks, so a screen
    /// that read its sources only when it drew would go deaf for exactly as
    /// long as something covers it.
    fn pump(&mut self, _at: f64) -> bool {
        false
    }

    /// Take a handle that wakes the loop from any thread. A screen with a
    /// source of its own hands it to that source, so a delivery wakes the
    /// loop the moment it lands and [`Screen::pump`] folds it in
    /// milliseconds. Without the wake, a message waits for the next
    /// scheduled second, and a person's press shows up to a second late.
    fn wake_by(&mut self, _wake: media_screen::Waker) {}

    /// Move the screen's clock to `at` seconds since the first frame. Every
    /// animation reads that clock, so a frame is a pure function of it.
    fn tick(&mut self, at: f64);

    /// The view for the clock's current position.
    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer>;

    /// The second at which the screen next changes, on the same clock
    /// [`Screen::tick`] reads. `at` is what that clock reads now, at or after
    /// the second of the last frame. The harness sleeps until the second this
    /// answer names, so a screen whose clock draws no seconds redraws once a
    /// minute rather than sixty times a second.
    ///
    /// `None` says nothing on this screen is scheduled, and the loop then
    /// draws on an event alone. A screen that folds in a source of its own,
    /// such as a bus, must not answer `None`.
    ///
    /// The default answers `at`, which is a change on the frame already drawn,
    /// so a screen that states nothing draws every pass the loop takes.
    fn next_frame(&self, at: f64) -> Option<f64> {
        Some(at)
    }

    /// Handle a message from a widget.
    fn update(&mut self, _message: Self::Message) {}

    /// Whether the screen asked for a fresh Wayland surface. The harness reads
    /// this on every wake of the loop, and the read clears the request, so one
    /// ask maps one new surface.
    fn surface_due(&mut self) -> bool {
        false
    }

    /// The new surface is up, on the frame at `at` seconds. Every motion that
    /// starts with the surface reads that second.
    fn surfaced(&mut self, _at: f64) {}
}

/// Run a screen until the run ends, and write what it measured.
pub fn run<S: Screen + 'static>(mut screen: S, options: Options) -> Result<(), String> {
    // The brand's two faces go into the toolkit's shared font system before
    // anything shapes a run of text, so every line the screen draws is
    // shaped against the face the binary carries and not against a fallback.
    liken_iced::font::load();

    // Two spans start here. The watchdog's grace runs from this moment, and so
    // does the time to the first frame, which covers the whole life of the
    // process: the wgpu setup, the first window, and the first draw.
    let launched = std::time::Instant::now();
    let watchdog = Watchdog::new(options.window_grace, launched);

    let event_loop = match EventLoop::new() {
        Ok(event_loop) => event_loop,
        // A client with no connection to a compositor has no window.
        Err(error) => {
            watchdog.expire(&format!("no connection to the compositor: {error}"));
            return Err(error.to_string());
        }
    };

    // The screen's own sources wake the loop through this proxy, so a bus
    // delivery folds the moment it lands. The event it sends carries
    // nothing: the wake is the message, and `about_to_wait` pumps on it.
    let proxy = event_loop.create_proxy();
    screen.wake_by(std::sync::Arc::new(move || {
        let _ = proxy.send_event(());
    }));

    let mut app = App {
        watchdog,
        stopped: false,
        state: State::Loading {
            screen: Some(screen),
            options,
            #[cfg(feature = "measure")]
            launched,
        },
    };

    let outcome = event_loop.run_app(&mut app);
    if app.stopped {
        return outcome.map_err(|error| error.to_string());
    }

    // The loop ended before the run did. The Wayland connection is the
    // window's one path to the screen, so a loop that ends any other way is a
    // window that went away, whether winit reported an error or not. Nothing
    // inside this process opens the connection again.
    let reason = match outcome {
        Ok(()) => "the compositor stopped answering".to_string(),
        Err(error) => format!("the compositor stopped answering: {error}"),
    };
    app.watchdog.expire(&reason);
    Err(reason)
}

/// The run, once the compositor has given the process a window.
pub struct Ready<S: Screen> {
    pub(crate) screen: S,
    pub(crate) timeline: Timeline,
    /// The window and everything the frame loop draws through. They arrive
    /// together and they go together, and a `present` replaces two of them at
    /// once, so the run holds the whole of what the compositor gave it.
    pub(crate) graphics: Graphics,
    /// The Wayland app-id every window this run maps asks for, including the
    /// one a `present` maps in place of the first.
    pub(crate) app_id: String,
    pub(crate) viewport: Viewport,
    pub(crate) cache: user_interface::Cache,
    pub(crate) clipboard: Clipboard,
    pub(crate) modifiers: ModifiersState,
    pub(crate) events: Vec<Event>,
    pub(crate) resized: bool,
    /// Whether a fresh Wayland surface is owed. The screen's ask moves here
    /// and stays until a map succeeds, so a compositor that gives no window
    /// on one wake is asked again on the next.
    pub(crate) surface_pending: bool,
    /// The second the screen named for its next change, while the loop sleeps
    /// toward it. The harness holds that second rather than asking again on
    /// every pass, because a fresh answer names the change after it and the
    /// frame would never be drawn.
    pub(crate) scheduled: Option<f64>,
    /// The timeline's zero: the first frame, not the launch. A compositor
    /// can take seconds to give a window, and a script or a capture that
    /// counted from the launch would fire on the first frame, before a
    /// resize arrived and before anything was drawn.
    pub(crate) start: Option<std::time::Instant>,
    /// The second of the last frame. The pace holds the next one at least
    /// [`frame::STEP`] after it, because nothing else caps the rate: the
    /// surface presents without vsync, so an animation that asked for a
    /// frame on every pass would draw as fast as the loop can spin.
    pub(crate) drawn: f64,
    /// Whether the frame on the glass shows old state. A fold and a key both
    /// set it, because the elements schedule their own motion and not the
    /// content: a level that changes while its row stands still would
    /// otherwise wait for the next scheduled second, and a press must show
    /// on the next frame.
    pub(crate) stale: bool,
    /// The frames this run writes to disk, from `--capture`. A run that named
    /// no directory captures nothing.
    #[cfg(feature = "measure")]
    pub(crate) captures: Option<Captures>,
    /// What this run measures, from `--stats`. A run that named no file
    /// measures nothing, and every call on the frame path is then one branch
    /// and no allocation.
    #[cfg(feature = "measure")]
    pub(crate) stats: Option<Stats>,
    pub(crate) finished: bool,
    /// Whether this run asked to leave the loop. The run reads it after the
    /// loop returns, so it tells its own end from a lost window.
    pub(crate) stopped: bool,
}

/// The run and the one thing that outlives its window: the watchdog, which
/// runs before the first window and after the last one.
struct App<S: Screen> {
    watchdog: Watchdog,
    /// Whether the run ended on its own terms: the quit key, the deadline, the
    /// last capture, or a close the compositor asked for.
    stopped: bool,
    state: State<S>,
}

enum State<S: Screen> {
    Loading {
        screen: Option<S>,
        options: Options,
        #[cfg(feature = "measure")]
        launched: std::time::Instant,
    },
    Ready(Box<Ready<S>>),
    /// The run is over and the graphics are already gone.
    Done,
}

impl<S: Screen> winit::application::ApplicationHandler for App<S> {
    fn resumed(&mut self, event_loop: &winit::event_loop::ActiveEventLoop) {
        let App {
            watchdog, state, ..
        } = self;
        let State::Loading {
            screen,
            options,
            #[cfg(feature = "measure")]
            launched,
        } = state
        else {
            return;
        };
        #[cfg(feature = "measure")]
        let launched = *launched;

        // The window comes first, so a compositor that gives none leaves the
        // screen and the flags where they are and the watchdog running.
        let Some(graphics) = graphics::open(event_loop, options.size, &options.app_id) else {
            return;
        };
        watchdog.present();

        let screen = screen.take().expect("one window per run");
        let viewport = Viewport::with_physical_size(
            Size::new(graphics.size.0, graphics.size.1),
            graphics.window.scale_factor() as f32,
        );
        #[cfg(feature = "measure")]
        let stats = Stats::requested(
            options.stats.take(),
            launched,
            graphics.backend.clone(),
            graphics.adapter.clone(),
            graphics.size,
        );
        #[cfg(feature = "measure")]
        let captures = Captures::requested(
            options.capture_dir.take(),
            std::mem::take(&mut options.capture_at),
        );

        let Options {
            script,
            quit_after,
            app_id,
            ..
        } = std::mem::take(options);

        *state = State::Ready(Box::new(Ready {
            screen,
            timeline: Timeline::new(script, quit_after),
            graphics,
            app_id,
            viewport,
            cache: user_interface::Cache::new(),
            clipboard: Clipboard::unconnected(),
            modifiers: ModifiersState::default(),
            events: Vec::new(),
            resized: false,
            surface_pending: false,
            scheduled: None,
            start: None,
            drawn: 0.0,
            stale: false,
            #[cfg(feature = "measure")]
            captures,
            #[cfg(feature = "measure")]
            stats,
            finished: false,
            stopped: false,
        }));
    }

    fn window_event(
        &mut self,
        event_loop: &winit::event_loop::ActiveEventLoop,
        id: winit::window::WindowId,
        event: WindowEvent,
    ) {
        let App {
            watchdog, state, ..
        } = self;
        let State::Ready(ready) = state else {
            return;
        };

        match &event {
            WindowEvent::RedrawRequested => {
                ready.frame(event_loop);
                return;
            }
            // A `present` maps a new window before it drops the old one, so
            // this arrives for a window the run has already replaced. The
            // watchdog reads only the window the run draws on now.
            WindowEvent::Destroyed if id == ready.graphics.window.id() => {
                watchdog.missing(std::time::Instant::now());
                return;
            }
            WindowEvent::Resized(_) => ready.resized = true,
            WindowEvent::CloseRequested => {
                ready.stop(event_loop);
                return;
            }
            WindowEvent::ModifiersChanged(new) => ready.modifiers = new.state(),
            WindowEvent::KeyboardInput { event, .. } => {
                if event.state.is_pressed()
                    && let Some(name) = key_name(&event.logical_key)
                    && ready.press(&name)
                {
                    ready.stop(event_loop);
                    return;
                }
            }
            _ => {}
        }

        if let Some(event) = conversion::window_event(
            event,
            ready.graphics.window.scale_factor() as f32,
            ready.modifiers,
        ) {
            ready.events.push(event);
        }
    }

    fn about_to_wait(&mut self, event_loop: &winit::event_loop::ActiveEventLoop) {
        // The grace is checked here rather than in the frame, because a client
        // with no window draws no frame.
        self.watchdog.expire_if_late(std::time::Instant::now());

        // A client waiting for a window has nothing to draw and a grace to
        // check, so the loop takes every pass it can until one is up. Winit
        // waits for an event otherwise, and a compositor that gives no window
        // sends none.
        if self.watchdog.counting() {
            event_loop.set_control_flow(ControlFlow::Poll);
            return;
        }

        let State::Ready(ready) = &mut self.state else {
            return;
        };

        // The deadline is checked here as well as in the frame, so a run ends
        // even if the compositor stops asking for frames. Before the first
        // frame there is no timeline yet, so there is no deadline.
        if let Some(start) = ready.start
            && ready.timeline.past_deadline(start.elapsed().as_secs_f64())
        {
            ready.stop(event_loop);
            return;
        }

        // The sources are pumped here, on every wake of the loop, because a
        // covered client draws no frame: the compositor sends a hidden
        // surface no frame callbacks. `present` is the one message that lets
        // a covered client map the surface that reveals it, so the bus must
        // be read on a path the compositor cannot starve.
        if let Some(start) = ready.start {
            let at = start.elapsed().as_secs_f64();
            if ready.screen.pump(at) {
                // What arrived changed the unit, so the frame on the glass is
                // stale whatever the animations schedule, and the second
                // scheduled before it no longer holds.
                ready.scheduled = None;
                ready.stale = true;
            }
            if ready.screen.surface_due() {
                ready.surface_pending = true;
            }
            if ready.surface_pending && ready.represent(event_loop) {
                ready.surface_pending = false;
                ready.screen.surfaced(at);
                // A Wayland surface is not on screen until its first buffer
                // arrives, so the new window gets a draw whatever the
                // schedule says.
                ready.graphics.window.request_redraw();
            }
        }

        ready.pace(event_loop);
    }

    fn exiting(&mut self, _event_loop: &winit::event_loop::ActiveEventLoop) {
        let App { stopped, state, .. } = self;
        if let State::Ready(ready) = state {
            ready.finish();
            *stopped = ready.stopped;
        }
        // wgpu builds an instance for every backend it can reach, and the one
        // for GL holds an EGL display on the compositor's connection. Its
        // destructor speaks Wayland, so it has to run while the connection is
        // open. winit closes the connection after this call and never before
        // it, so the graphics are dropped here rather than where the loop
        // returns.
        self.state = State::Done;
    }
}

/// The script's name for a key. A letter or a digit is itself, and the arrows
/// carry the names a scripted timeline uses.
pub fn key_name(key: &Key) -> Option<String> {
    match key {
        Key::Character(text) => Some(text.to_lowercase()),
        Key::Named(NamedKey::ArrowUp) => Some("up".into()),
        Key::Named(NamedKey::ArrowDown) => Some("down".into()),
        Key::Named(NamedKey::ArrowLeft) => Some("left".into()),
        Key::Named(NamedKey::ArrowRight) => Some("right".into()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced_winit::winit::keyboard::SmolStr;

    #[test]
    fn a_letter_names_itself() {
        assert_eq!(
            key_name(&Key::Character(SmolStr::new("Q"))),
            Some("q".to_string())
        );
    }

    #[test]
    fn an_arrow_carries_the_script_name() {
        assert_eq!(
            key_name(&Key::Named(NamedKey::ArrowLeft)),
            Some("left".to_string())
        );
    }

    #[test]
    fn a_key_with_no_script_name_is_ignored() {
        assert_eq!(key_name(&Key::Named(NamedKey::F1)), None);
    }
}
