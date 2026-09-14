//! The display's own Wayland surface, and what it draws on it.
//!
//! The window is 1920 by 1080 in logical pixels, undecorated, and
//! transparent. Between summons it draws nothing, and a frame that draws
//! nothing is a fully transparent frame.

use std::time::Duration;

use iced::widget::canvas;
use iced::{Color, Element, Length, Rectangle, Renderer, Size, Subscription, Task, Theme};

use crate::canvas::{Canvas, Scrim};
use crate::fade::Fade;
use crate::ipc::{self, Ipc};
use crate::theme;

/// How long the display waits for mpv to report a position before it exits
/// and lets the kubelet restart the container. It is longer than a player
/// takes to open a film and shorter than a person waits at a bare picture.
pub const PLAYER_GRACE: Duration = Duration::from_secs(60);

/// How many messages the socket reader may run ahead of the frame loop.
const EVENT_QUEUE: usize = 64;

/// What moves the display.
#[derive(Debug, Clone)]
pub enum Message {
    /// One line mpv wrote.
    Mpv(ipc::Event),
    /// One step of the fade.
    Tick,
    /// The idle window ran out. The count says which summon armed it, so a
    /// window a later summon replaced dismisses nothing.
    Hide(u64),
}

/// The whole of the display's state so far: whether it stands summoned, how
/// far the fade has moved, and whether the film is paused.
#[derive(Debug, Default)]
pub struct Display {
    ipc: Ipc,
    fade: Fade,
    summoned: bool,
    /// How many summons have landed. The count names each idle window.
    summons: u64,
    /// The idle window that stands, if one does. Arming a window replaces
    /// what stood, and a dismiss cancels it.
    armed: Option<u64>,
    paused: bool,
    /// mpv reports the pause state once when the display observes it. The
    /// first report only records it, so a film that loads paused starts with
    /// the display hidden.
    first_pause: bool,
}

impl Display {
    pub fn new(ipc: Ipc) -> Self {
        Self {
            ipc,
            first_pause: true,
            ..Self::default()
        }
    }

    pub fn update(&mut self, message: Message) -> Task<Message> {
        match message {
            Message::Mpv(ipc::Event::Message(arguments)) if ipc::summons(&arguments) => {
                return self.summon();
            }
            Message::Mpv(ipc::Event::Property { name, value }) if name == "pause" => {
                return self.on_pause(value.as_bool().unwrap_or(false));
            }
            Message::Mpv(_) => {}
            Message::Tick => self.fade.step(),
            Message::Hide(summons) if self.armed == Some(summons) => self.dismiss(),
            Message::Hide(_) => {}
        }
        Task::none()
    }

    /// Show the display and arm the idle window. It moves no focus.
    fn summon(&mut self) -> Task<Message> {
        self.summoned = true;
        self.fade.to(1.0);
        self.arm_hide()
    }

    /// Clear the display and cancel the idle window.
    fn dismiss(&mut self) {
        self.summoned = false;
        self.armed = None;
        self.fade.to(0.0);
    }

    /// A pause summons the display and holds it, and a resume dismisses it at
    /// once.
    fn on_pause(&mut self, paused: bool) -> Task<Message> {
        self.paused = paused;
        if self.first_pause {
            self.first_pause = false;
            return Task::none();
        }
        if paused {
            return self.summon();
        }
        if self.summoned {
            self.dismiss();
        }
        Task::none()
    }

    /// Arm the idle window on this summon. A summon while paused arms
    /// nothing, so the display stands for as long as the film does.
    fn arm_hide(&mut self) -> Task<Message> {
        self.summons += 1;
        self.armed = None;
        if self.paused {
            return Task::none();
        }
        let summons = self.summons;
        self.armed = Some(summons);
        Task::perform(tokio::time::sleep(theme::IDLE_HIDE), move |()| {
            Message::Hide(summons)
        })
    }

    pub fn view(&self) -> Element<'_, Message> {
        canvas(Scrims {
            fade: self.fade.value(),
        })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
    }

    /// The socket reader always runs. The frame tick runs only while the
    /// fade is moving, so a display standing at either end costs the process
    /// nothing.
    pub fn subscription(&self) -> Subscription<Message> {
        let mpv = Subscription::run_with(self.ipc.path().to_path_buf(), |path| {
            let ipc = Ipc::at(path);
            iced::stream::channel(EVENT_QUEUE, async move |mut output| {
                use iced::futures::SinkExt;

                let (sender, mut events) = tokio::sync::mpsc::channel(EVENT_QUEUE);
                tokio::spawn(async move { ipc.serve(sender).await });
                while let Some(event) = events.recv().await {
                    if output.send(Message::Mpv(event)).await.is_err() {
                        return;
                    }
                }
            })
        });
        if !self.fade.running() {
            return mpv;
        }
        Subscription::batch([
            mpv,
            iced::time::every(theme::FADE_TICK).map(|_| Message::Tick),
        ])
    }
}

/// The two scrims, and nothing else yet.
struct Scrims {
    fade: f32,
}

impl canvas::Program<Message> for Scrims {
    type State = ();

    fn draw(
        &self,
        _state: &(),
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: iced::mouse::Cursor,
    ) -> Vec<canvas::Geometry> {
        // A frame with nothing in it is what the display draws between
        // summons: the film below shows through every pixel of the window.
        if self.fade <= 0.0 {
            return Vec::new();
        }

        // The size comes from the surface the compositor configured, not
        // from the 1920 by 1080 the window asked for, because the region a
        // Layout gives this pod is the compositor's choice and not this
        // client's.
        let canvas = Canvas::for_output(bounds.size());
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        frame.scale(canvas.scale);
        Scrim::top().draw(&mut frame, &canvas, self.fade);
        Scrim::bottom().draw(&mut frame, &canvas, self.fade);
        vec![frame.into_geometry()]
    }
}

/// Open the window and run the frame loop. The caller waits for mpv first,
/// so the surface this opens arrives after the film's.
pub fn run(ipc: Ipc) -> iced::Result {
    iced::application(
        move || Display::new(ipc.clone()),
        Display::update,
        Display::view,
    )
    .subscription(Display::subscription)
    // The window is transparent, so the whole style is the ground it
    // does not paint.
    .style(|_display, _theme| iced::theme::Style {
        background_color: Color::TRANSPARENT,
        text_color: theme::color::text(),
    })
    .window(iced::window::Settings {
        size: Size::new(theme::CANVAS_WIDTH, theme::CANVAS_HEIGHT),
        decorations: false,
        transparent: true,
        resizable: false,
        ..Default::default()
    })
    // The brand's two faces come out of the binary, not out of whatever
    // the image has installed, so every liken display draws the same
    // face.
    .font(liken_iced::font::REGULAR_OTF)
    .font(liken_iced::font::ITALIC_OTF)
    .default_font(liken_iced::font::REGULAR)
    .title("liken display")
    .run()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn message(word: &str) -> Message {
        Message::Mpv(ipc::Event::Message(vec![word.to_string()]))
    }

    fn pause(paused: bool) -> Message {
        Message::Mpv(ipc::Event::Property {
            name: "pause".to_string(),
            value: json!(paused),
        })
    }

    /// Run the fade to wherever it is going, the way the tick does.
    fn settle(display: &mut Display) {
        while display.fade.running() {
            let _ = display.update(Message::Tick);
        }
    }

    #[test]
    fn the_display_starts_clear() {
        let display = Display::new(Ipc::default());
        assert_eq!(display.fade.value(), 0.0);
        assert!(!display.summoned);
    }

    #[tokio::test]
    async fn the_summon_and_the_six_actions_raise_the_display() {
        for word in ["summon", "up", "down", "left", "right", "select", "back"] {
            let mut display = Display::new(Ipc::default());
            let _ = display.update(message(word));
            settle(&mut display);
            assert!(display.summoned, "{word} raises the display");
            assert_eq!(display.fade.value(), 1.0);
        }
    }

    #[test]
    fn a_message_the_display_has_no_case_for_raises_nothing() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(message("liken-art"));
        assert!(!display.summoned);
        assert_eq!(display.fade.value(), 0.0);
    }

    #[tokio::test]
    async fn the_idle_window_dismisses_the_display() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(message("summon"));
        settle(&mut display);
        let armed = display.armed.expect("a summon arms an idle window");

        let _ = display.update(Message::Hide(armed));
        settle(&mut display);
        assert!(!display.summoned);
        assert_eq!(display.fade.value(), 0.0);
    }

    /// A second summon arms a second window, and the first one's expiry
    /// dismisses nothing.
    #[tokio::test]
    async fn a_stale_idle_window_dismisses_nothing() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(message("summon"));
        let first = display.armed.expect("a summon arms an idle window");
        let _ = display.update(message("up"));
        settle(&mut display);

        assert_ne!(display.armed, Some(first));
        let _ = display.update(Message::Hide(first));
        assert!(display.summoned);
        assert_eq!(display.fade.value(), 1.0);
    }

    /// mpv reports the pause state once at registration. The first report
    /// only records it, so a film that loads paused starts hidden.
    #[test]
    fn a_film_that_loads_paused_starts_hidden() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(pause(true));
        settle(&mut display);
        assert!(!display.summoned);
        assert_eq!(display.fade.value(), 0.0);
        assert!(display.paused);
    }

    #[tokio::test]
    async fn a_pause_raises_the_display_and_arms_no_idle_window() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(pause(false));
        let _ = display.update(pause(true));
        settle(&mut display);
        assert!(display.summoned);
        assert_eq!(display.fade.value(), 1.0);
        assert_eq!(display.armed, None);
    }

    #[tokio::test]
    async fn a_resume_dismisses_the_display_at_once() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(pause(false));
        let _ = display.update(pause(true));
        settle(&mut display);

        let _ = display.update(pause(false));
        settle(&mut display);
        assert!(!display.summoned);
        assert_eq!(display.fade.value(), 0.0);
    }

    #[test]
    fn a_resume_with_the_display_already_hidden_changes_nothing() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(pause(true));
        let _ = display.update(pause(false));
        settle(&mut display);
        assert!(!display.summoned);
        assert_eq!(display.fade.value(), 0.0);
    }

    /// A summon while the fade is falling reverses it in place, so the
    /// display never drops to nothing and rises again.
    #[tokio::test]
    async fn a_summon_during_a_fade_out_reverses_it() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(message("summon"));
        settle(&mut display);
        let armed = display.armed.expect("a summon arms an idle window");
        let _ = display.update(Message::Hide(armed));
        for _ in 0..18 {
            let _ = display.update(Message::Tick);
        }
        let standing = display.fade.value();
        assert!(standing > 0.0 && standing < 1.0);

        let _ = display.update(message("select"));
        assert!(display.fade.value() >= standing);
        settle(&mut display);
        assert_eq!(display.fade.value(), 1.0);
    }

    /// The socket reader always runs, and the frame tick joins it only
    /// while the fade is moving.
    #[tokio::test]
    async fn the_frame_tick_runs_only_while_the_fade_moves() {
        let mut display = Display::new(Ipc::default());
        assert!(!display.fade.running());
        let _ = display.subscription();

        let _ = display.update(message("summon"));
        assert!(display.fade.running());
        let _ = display.subscription();

        settle(&mut display);
        assert!(!display.fade.running());
        let _ = display.subscription();
    }

    /// The scrims draw nothing at a fade of nothing, and the two bands at
    /// anything above it.
    #[test]
    fn the_scrims_carry_the_fade_the_display_stands_at() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        for scrim in [Scrim::top(), Scrim::bottom()] {
            assert!(
                scrim
                    .bands(&canvas, 0.0)
                    .iter()
                    .all(|band| band.stops.iter().all(|alpha| *alpha == 0.0))
            );
            assert!(
                scrim
                    .bands(&canvas, 1.0)
                    .iter()
                    .any(|band| band.stops.iter().any(|alpha| *alpha > 0.0))
            );
        }
    }

    #[test]
    fn a_property_the_display_has_no_case_for_moves_nothing() {
        let mut display = Display::new(Ipc::default());
        let _ = display.update(Message::Mpv(ipc::Event::Property {
            name: "time-pos".to_string(),
            value: json!(12.5),
        }));
        let _ = display.update(Message::Mpv(ipc::Event::Detached));
        assert!(!display.summoned);
        assert_eq!(display.fade.value(), 0.0);
    }
}
