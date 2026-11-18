//! Runs a [`Screen`] in a Wayland window: the command-line flags, the frame
//! loop, and the optional frame capture and statistics.
//!
//! The harness runs its own winit event loop instead of calling
//! `iced::application`, because frame capture and the timing of each frame
//! need the renderer, and `iced::application` does not expose it.

pub mod frame;
pub mod graphics;
pub mod options;
pub mod respawn;
pub mod timeline;
pub mod watchdog;

// Capture and statistics are turned on only by command-line flags. The
// operator passes no flags to the idle container, so `Dockerfile.screen`
// builds the image without the `measure` feature.
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

/// A screen the harness can run. The harness advances the screen's clock,
/// passes it each key from the script or the keyboard, and asks it for a view
/// to draw.
pub trait Screen {
    /// The messages the screen's own widgets emit.
    type Message: std::fmt::Debug + Send + 'static;

    /// The clear color of each frame, which also fills a captured frame.
    fn background(&self) -> Color {
        Color::BLACK
    }

    /// Handles one key press. `name` is a script key, or the name that
    /// [`key_name`] gives a key of the keyboard.
    fn key(&mut self, name: &str);

    /// Applies the messages that the screen's own sources, such as the bus,
    /// received since the last call. Returns true when the view changed, so
    /// the harness redraws and asks [`Screen::next_frame`] again.
    ///
    /// The harness calls this on every wake of the loop, not only when it
    /// draws, because a compositor can stop sending frame callbacks to a
    /// covered Wayland surface. A screen that read its sources only while
    /// drawing would miss each message, such as the end of a film, that
    /// arrives while a Play covers the idle screen.
    fn pump(&mut self, _at: f64) -> bool {
        false
    }

    /// Receives a waker that wakes the event loop from any thread. A screen
    /// gives it to each of its sources, so [`Screen::pump`] applies a message
    /// as soon as it arrives. Without it, a message, such as a remote's key
    /// press, waits up to [`timeline::BACKSTOP`] seconds for the next wake.
    fn wake_by(&mut self, _wake: media_screen::Waker) {}

    /// Sets the screen's clock to `at` seconds since the first frame. The
    /// screen's animations read this clock and not the system time, so the
    /// view depends only on the clock and the screen's state.
    fn tick(&mut self, at: f64);

    /// The view for the clock's current time.
    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer>;

    /// Returns the next second on the screen's clock at which the view
    /// changes, and the harness sleeps until then. A screen that shows a
    /// clock with no seconds returns the next minute.
    ///
    /// `at` is the second of the last frame drawn, not the current clock.
    /// [`frame::screen_due`] gives the reason.
    ///
    /// `None` means the loop draws only when an event arrives, and it also
    /// stops the [`timeline::BACKSTOP`] wake. So a screen with sources of
    /// its own must not return `None`.
    ///
    /// The default returns `at`, so a screen that does not override it is
    /// drawn on every pass of the loop, at most once each [`frame::STEP`].
    fn next_frame(&self, at: f64) -> Option<f64> {
        Some(at)
    }

    /// Handles a message that one of the screen's widgets emitted.
    fn update(&mut self, _message: Self::Message) {}
}

/// Runs a screen until the quit key, the `--quit-after` deadline, the last
/// capture, or a close request from the compositor ends the run. Returns an
/// error when the compositor goes away first.
pub fn run<S: Screen + 'static>(mut screen: S, options: Options) -> Result<(), String> {
    // The fonts must be loaded into iced's font system before any text is
    // shaped. Otherwise the first text is shaped with a fallback font.
    liken_iced::font::load();

    // The watchdog's grace period and the time-to-first-frame statistic both
    // count from here, so the statistic includes the wgpu setup, the window,
    // and the first draw.
    let launched = std::time::Instant::now();
    let watchdog = Watchdog::new(options.window_grace, launched);

    let event_loop = match EventLoop::new() {
        Ok(event_loop) => event_loop,
        Err(error) => {
            watchdog.expire(&format!("no connection to the compositor: {error}"));
            return Err(error.to_string());
        }
    };

    // The user event is empty, because waking the loop is enough:
    // `about_to_wait` then calls `Screen::pump`.
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

    // The event loop ended without a call to `Ready::stop`. The window
    // cannot exist without its Wayland connection, so the compositor went
    // away, whether or not winit returned an error. The process cannot
    // reconnect, so the watchdog exits it when a grace period is set, and
    // the supervisor in `respawn.rs` starts it again.
    let reason = match outcome {
        Ok(()) => "the compositor stopped answering".to_string(),
        Err(error) => format!("the compositor stopped answering: {error}"),
    };
    app.watchdog.expire(&reason);
    Err(reason)
}

/// The state of a run after the compositor has created the window.
pub struct Ready<S: Screen> {
    pub(crate) screen: S,
    pub(crate) timeline: Timeline,
    pub(crate) graphics: Graphics,
    pub(crate) viewport: Viewport,
    pub(crate) cache: user_interface::Cache,
    pub(crate) clipboard: Clipboard,
    pub(crate) modifiers: ModifiersState,
    pub(crate) events: Vec<Event>,
    pub(crate) resized: bool,
    /// The second that `Screen::next_frame` last returned, kept while the
    /// loop sleeps toward it. A key press or a change from `Screen::pump`
    /// clears it, because the screen's schedule can have changed.
    pub(crate) next_frame_at: Option<f64>,
    /// When the first frame was drawn. The screen's clock, the script, and
    /// the captures count from here and not from the launch, because a
    /// compositor can take seconds to create the window. Counted from the
    /// launch, the early keys and captures would all fire on the first frame.
    pub(crate) start: Option<std::time::Instant>,
    /// The second of the last frame drawn. The next frame is at least
    /// [`frame::STEP`] later.
    pub(crate) last_frame_at: f64,
    /// Set by a key press or a change from `Screen::pump`, because
    /// `Screen::next_frame` schedules animations and not changes of content.
    /// Without it, a bar that changes length while nothing animates is not
    /// redrawn until the next scheduled second.
    pub(crate) needs_redraw: bool,
    /// The frames this run writes to disk, from `--capture`. `None` when the
    /// flag is absent.
    #[cfg(feature = "measure")]
    pub(crate) captures: Option<Captures>,
    /// The measurements of this run, from `--stats`. `None` when the flag is
    /// absent, so a run without it records nothing.
    #[cfg(feature = "measure")]
    pub(crate) stats: Option<Stats>,
    pub(crate) finished: bool,
    /// Set by [`Ready::stop`]. `exiting` copies it to `App::stopped`.
    pub(crate) stopped: bool,
}

/// The watchdog is outside [`State`] because it must run before the window
/// exists and after it is destroyed.
struct App<S: Screen> {
    watchdog: Watchdog,
    /// True when the run ended through [`Ready::stop`]. `run` reads it to
    /// tell a requested exit from a lost compositor.
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
    /// The run is over and the graphics are already dropped.
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

        // With no window the state stays `Loading`, and the watchdog keeps
        // counting its grace period.
        let Some(graphics) = graphics::open(event_loop, options.size) else {
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
            script, quit_after, ..
        } = std::mem::take(options);

        *state = State::Ready(Box::new(Ready {
            screen,
            timeline: Timeline::new(script, quit_after),
            graphics,
            viewport,
            cache: user_interface::Cache::new(),
            clipboard: Clipboard::unconnected(),
            modifiers: ModifiersState::default(),
            events: Vec::new(),
            resized: false,
            next_frame_at: None,
            start: None,
            last_frame_at: 0.0,
            needs_redraw: false,
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
            // The run never creates a second window, so when this one is
            // destroyed the watchdog starts its grace period again.
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
                    && ready.keyboard(&name)
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
        // The grace period is checked here and not in a frame, because a
        // process with no window draws no frames.
        self.watchdog.expire_if_late(std::time::Instant::now());

        // While the grace period runs there is no window to draw. A
        // compositor that never creates the window sends no event, so the
        // loop sleeps until the grace period ends and the check above runs.
        if let Some(deadline) = self.watchdog.deadline() {
            event_loop.set_control_flow(ControlFlow::WaitUntil(deadline));
            return;
        }

        let State::Ready(ready) = &mut self.state else {
            return;
        };

        // The deadline is checked here as well as in the frame, so the run
        // ends even when the compositor stops sending frame callbacks.
        if let Some(start) = ready.start
            && ready.timeline.past_deadline(start.elapsed().as_secs_f64())
        {
            ready.stop(event_loop);
            return;
        }

        // On every wake, not only in a frame. `Screen::pump` gives the reason.
        if let Some(start) = ready.start {
            let at = start.elapsed().as_secs_f64();
            if ready.screen.pump(at) {
                ready.next_frame_at = None;
                ready.needs_redraw = true;
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
        // wgpu probes every backend, and its GL backend creates an EGL
        // display on the compositor's Wayland connection. Dropping that
        // display sends Wayland requests, so it must happen before winit
        // closes the connection after `exiting` returns. The device and the
        // surface hold the last references to it, so the graphics are
        // dropped here and not after the loop.
        self.state = State::Done;
    }
}

/// The name a script uses for a key: the key's text in lower case for a
/// character key, or `up`, `down`, `left`, `right` for the arrow keys. Other
/// keys return `None`.
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
    fn a_letter_key_is_named_by_its_lowercase_letter() {
        assert_eq!(
            key_name(&Key::Character(SmolStr::new("Q"))),
            Some("q".to_string())
        );
    }

    #[test]
    fn an_arrow_key_has_its_script_name() {
        assert_eq!(
            key_name(&Key::Named(NamedKey::ArrowLeft)),
            Some("left".to_string())
        );
    }

    #[test]
    fn a_key_with_no_script_name_returns_none() {
        assert_eq!(key_name(&Key::Named(NamedKey::F1)), None);
    }
}
