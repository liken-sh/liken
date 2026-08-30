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

pub mod frame;
pub mod graphics;
pub mod options;
pub mod stats;
pub mod timeline;

use std::path::PathBuf;
use std::sync::Arc;

use iced_wgpu::graphics::Viewport;
use iced_wgpu::{Renderer, wgpu};
use iced_winit::core::{Color, Element, Event, Size, Theme};
use iced_winit::runtime::user_interface;
use iced_winit::winit;
use iced_winit::{Clipboard, conversion};

use winit::event::WindowEvent;
use winit::event_loop::{ControlFlow, EventLoop};
use winit::keyboard::{Key, ModifiersState, NamedKey};

pub use options::{Invocation, Options};
use stats::Stats;
use timeline::Timeline;

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

    /// One key press, named the way the script names it: a single letter, or
    /// `up`, `down`, `left`, `right`.
    fn key(&mut self, name: &str);

    /// Move the screen's clock to `at` seconds since the process started. Every
    /// animation reads that clock, so a frame is a pure function of it.
    fn tick(&mut self, at: f64);

    /// The view for the clock's current position.
    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer>;

    /// Handle a message from a widget.
    fn update(&mut self, _message: Self::Message) {}
}

/// Run a screen to the end of its script and write what it measured.
pub fn run<S: Screen + 'static>(screen: S, options: Options) -> Result<(), String> {
    // The clock starts here, so every measured time counts the whole life of
    // the process: the wgpu setup, the first window, and the first frame.
    let started = std::time::Instant::now();

    let event_loop = EventLoop::new().map_err(|error| error.to_string())?;
    let mut app = App::Loading {
        screen: Some(screen),
        options,
        started,
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
    pub(crate) start: std::time::Instant,
    pub(crate) stats: Stats,
    pub(crate) finished: bool,
}

enum App<S: Screen> {
    Loading {
        screen: Option<S>,
        options: Options,
        started: std::time::Instant,
    },
    Ready(Box<Ready<S>>),
    /// The run is over and the graphics are already gone.
    Done,
}

impl<S: Screen> winit::application::ApplicationHandler for App<S> {
    fn resumed(&mut self, event_loop: &winit::event_loop::ActiveEventLoop) {
        let Self::Loading {
            screen,
            options,
            started,
        } = self
        else {
            return;
        };
        let started = *started;
        let screen = screen.take().expect("one window per run");
        let Options {
            script,
            capture_dir,
            capture_at,
            stats: stats_path,
            quit_after,
            size,
        } = std::mem::take(options);

        let graphics = graphics::open(event_loop, size);
        let viewport = Viewport::with_physical_size(
            Size::new(graphics.size.0, graphics.size.1),
            graphics.window.scale_factor() as f32,
        );
        let stats = Stats::new(
            graphics.backend.clone(),
            graphics.adapter.clone(),
            graphics.size,
        );

        // Poll rather than Wait, because the screen animates on its own clock
        // and no input arrives under the headless backend.
        event_loop.set_control_flow(ControlFlow::Poll);

        *self = Self::Ready(Box::new(Ready {
            screen,
            timeline: Timeline::new(script, capture_dir, capture_at, quit_after),
            stats_path,
            window: graphics.window,
            device: graphics.device,
            surface: graphics.surface,
            format: graphics.format,
            renderer: graphics.renderer,
            viewport,
            cache: user_interface::Cache::new(),
            clipboard: Clipboard::unconnected(),
            modifiers: ModifiersState::default(),
            events: Vec::new(),
            resized: false,
            start: started,
            stats,
            finished: false,
        }));
    }

    fn window_event(
        &mut self,
        event_loop: &winit::event_loop::ActiveEventLoop,
        _id: winit::window::WindowId,
        event: WindowEvent,
    ) {
        let Self::Ready(ready) = self else {
            return;
        };

        match &event {
            WindowEvent::RedrawRequested => {
                ready.frame(event_loop);
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

        if let Some(event) =
            conversion::window_event(event, ready.window.scale_factor() as f32, ready.modifiers)
        {
            ready.events.push(event);
        }
    }

    fn about_to_wait(&mut self, event_loop: &winit::event_loop::ActiveEventLoop) {
        let Self::Ready(ready) = self else {
            return;
        };

        // The deadline is checked here as well as in the frame, so a run ends
        // even if the compositor stops asking for frames.
        if ready
            .timeline
            .past_deadline(ready.start.elapsed().as_secs_f64())
        {
            ready.stop(event_loop);
            return;
        }

        ready.window.request_redraw();
    }

    fn exiting(&mut self, _event_loop: &winit::event_loop::ActiveEventLoop) {
        if let Self::Ready(ready) = self {
            ready.finish();
        }
        // wgpu builds an instance for every backend it can reach, and the one
        // for GL holds an EGL display on the compositor's connection. Its
        // destructor speaks Wayland, so it has to run while the connection is
        // open. winit closes the connection after this call and never before
        // it, so the graphics are dropped here rather than where the loop
        // returns.
        *self = Self::Done;
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
