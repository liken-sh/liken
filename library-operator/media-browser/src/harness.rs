//! The media browser's harness: the flags, the frame loop, and the
//! measurements.
//!
//! A screen here is a piece of state with a clock and a view. The harness owns
//! everything around it: the winit window, the wgpu surface, the iced renderer,
//! the scripted key timeline, the frame capture, and the statistics file.
//!
//! The harness drives its own winit loop instead of calling `iced::application`
//! because it must reach the renderer directly. A frame capture and a frame
//! clock are not part of the high-level entry point.

mod app;
pub mod capture;
pub mod frame;
pub mod graphics;
pub mod options;
pub mod stats;
pub mod timeline;
pub mod watchdog;

use std::path::PathBuf;
use std::sync::Arc;

use iced_wgpu::graphics::Viewport;
use iced_wgpu::{Renderer, wgpu};
use iced_winit::Clipboard;
use iced_winit::core::{Color, Element, Event, Theme};
use iced_winit::runtime::user_interface;
use iced_winit::winit;

use winit::event_loop::EventLoop;
use winit::keyboard::{Key, ModifiersState, NamedKey};

use app::{App, State};
use capture::Captures;
pub use options::{Invocation, Options};
use stats::Stats;
use timeline::Timeline;
use watchdog::Watchdog;

/// The key that ends a run, from the keyboard or from a script.
pub const QUIT: &str = "q";

/// A handle that wakes the screen's event loop from any thread. It is
/// the crate's own type, because the bus hands one back to the browser
/// through [`media_screen::Bus::wake_on_delivery`], and two aliases of
/// one shape would drift.
pub use media_screen::Waker;

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

    /// One key press, named the way the script names it: a single
    /// letter, or one of the names `key_name` gives the arrows,
    /// `enter`, `escape`, and `backspace`.
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
    fn wake_by(&mut self, _wake: Waker) {}

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

    /// The new surface is up, on the frame at `at` seconds.
    fn surfaced(&mut self, _at: f64) {}
}

/// Run a screen to the end of its script and write what it measured.
pub fn run<S: Screen + 'static>(mut screen: S, options: Options) -> Result<(), String> {
    // The brand's faces go into the toolkit's font system before anything
    // shapes a run of text. `views::text::measured` runs the shaper on the
    // read path, and a measurement taken against a fallback face would
    // place every line after it wrong.
    liken_iced::font::load();

    // The launch is measured from here, so the time to the first frame
    // counts the whole life of the process: the wgpu setup, the first
    // window, and the first draw.
    //
    // the watchdog's grace runs from the same moment, because a client
    // that never reaches a window has drawn nothing since the launch.
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

    // The screen's own sources wake the loop through this proxy, so a
    // delivery folds the moment it lands. The event it sends carries
    // nothing: the wake is the message, and `about_to_wait` pumps on it.
    // The browser hands it to its catalog source and its poster store.
    let proxy = event_loop.create_proxy();
    screen.wake_by(Arc::new(move || {
        let _ = proxy.send_event(());
    }));

    let mut app = App {
        watchdog,
        state: State::Loading {
            screen: Some(screen),
            // The options are boxed so the state of a run that has not
            // opened a window yet is no larger than the state of one that has.
            options: Box::new(options),
            launched,
        },
    };

    event_loop
        .run_app(&mut app)
        .map_err(|error| error.to_string())
}

/// The run, once the compositor has given the process a window.
pub struct Ready<S: Screen> {
    pub(crate) screen: S,
    pub(crate) timeline: Timeline,
    // Where the measurements go at exit, from --stats.
    pub(crate) stats_path: Option<PathBuf>,
    pub(crate) window: Arc<winit::window::Window>,
    /// The instance every surface of this run comes from, held because the
    /// re-present creates a second one.
    pub(crate) instance: wgpu::Instance,
    pub(crate) device: wgpu::Device,
    pub(crate) surface: wgpu::Surface<'static>,
    pub(crate) format: wgpu::TextureFormat,
    pub(crate) renderer: Renderer,
    pub(crate) viewport: Viewport,
    pub(crate) cache: user_interface::Cache,
    pub(crate) clipboard: Clipboard,
    pub(crate) modifiers: ModifiersState,
    pub(crate) events: Vec<Event>,
    pub(crate) resized: bool,
    /// The app-id every window of this run asks for, held because the
    /// re-present maps a second window.
    pub(crate) app_id: String,
    /// Whether a present is still waiting on a window the compositor has not
    /// given yet.
    pub(crate) surface_pending: bool,
    /// When the process began, for the time to the first frame.
    pub(crate) launched: std::time::Instant,
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
    pub(crate) captures: Option<Captures>,
    pub(crate) stats: Stats,
    pub(crate) finished: bool,
}

/// The script's name for a key. A letter or a digit is itself, and
/// the arrows, enter, escape, and backspace carry the names a scripted
/// timeline uses.
pub fn key_name(key: &Key) -> Option<String> {
    match key {
        Key::Character(text) => Some(text.to_lowercase()),
        Key::Named(NamedKey::ArrowUp) => Some("up".into()),
        Key::Named(NamedKey::ArrowDown) => Some("down".into()),
        Key::Named(NamedKey::ArrowLeft) => Some("left".into()),
        Key::Named(NamedKey::ArrowRight) => Some("right".into()),
        Key::Named(NamedKey::Enter) => Some("enter".into()),
        Key::Named(NamedKey::Escape) => Some("escape".into()),
        Key::Named(NamedKey::Backspace) => Some("backspace".into()),
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
    fn the_navigation_keys_carry_their_script_names() {
        assert_eq!(
            key_name(&Key::Named(NamedKey::Enter)),
            Some("enter".to_string())
        );
        assert_eq!(
            key_name(&Key::Named(NamedKey::Escape)),
            Some("escape".to_string())
        );
        assert_eq!(
            key_name(&Key::Named(NamedKey::Backspace)),
            Some("backspace".to_string())
        );
    }

    #[test]
    fn a_key_with_no_script_name_is_ignored() {
        assert_eq!(key_name(&Key::Named(NamedKey::F1)), None);
    }

    struct Still;

    impl Screen for Still {
        type Message = ();

        fn key(&mut self, _name: &str) {}

        fn tick(&mut self, _at: f64) {}

        fn view(&self) -> Element<'_, (), Theme, Renderer> {
            iced_widget::Space::new().into()
        }
    }

    #[test]
    fn a_screen_that_states_nothing_draws_on_black_folds_nothing_and_redraws_every_pass() {
        let mut still = Still;
        assert_eq!(still.background(), Color::BLACK);
        assert!(!still.pump(1.0));
        assert_eq!(still.next_frame(3.5), Some(3.5));
        assert!(!still.surface_due());
        still.wake_by(Arc::new(|| {}));
        still.update(());
        still.surfaced(2.0);
        still.key("q");
        still.tick(2.0);
    }
}
