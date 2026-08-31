// The client the idle pod runs. It reads the unit's state off the bus, holds
// it, and draws the idle screen.
//
// The screen draws the ground and nothing over it. What the client holds is
// `Unit`, and every element reads its facts and its seconds from there.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::Canvas;
use iced_winit::core::{Color, Element, Length, Theme};

use crate::bus::status::Activity;
use crate::bus::{self, Message, Reader};
use crate::harness::Screen;
use crate::idle::Idle;
use crate::idle::preview::Keys;
use crate::look;
use crate::unit::Unit;
use crate::wiring::Wiring;

#[derive(Debug)]
pub struct Client {
    unit: Unit,
    /// The subscription, or nothing when the operator named no broker. A run on
    /// a workstation draws the seeds alone.
    bus: Option<Reader>,
    /// Whether a `present` asked for a fresh Wayland surface. The harness
    /// reads it on every wake of the loop.
    surface_due: bool,
    /// The preview keys, on a run that binds them. They stand in for the bus
    /// on a workstation, and the legend draws where they are bound.
    keys: Option<Keys>,
    /// The second of the frame being drawn. The view is a function of it, so
    /// the tick records it and every element reads it.
    at: f64,
}

impl Client {
    /// The client one wiring describes. The binary reads the environment once
    /// and hands the whole of it here, so this file names no variable. The
    /// seeds name the unit before the broker answers, so the first frame is
    /// never blank.
    pub fn open(wiring: Wiring) -> Self {
        let client_id = bus::client_id(&bus::hostname());
        let keys = wiring
            .preview
            .then(|| Keys::seeded(wiring.player_name.clone(), wiring.components.clone()));
        Self {
            unit: Unit::seeded(wiring.player_name, wiring.components),
            bus: Reader::open(&wiring.bus_address, &client_id, wiring.topics),
            surface_due: false,
            keys,
            at: 0.0,
        }
    }

    /// The unit as the screen holds it.
    pub fn unit(&self) -> &Unit {
        &self.unit
    }

    /// The screen the client draws, at the second the clock last read. The
    /// view and the schedule read one screen, so what the harness sleeps
    /// toward is what the next frame draws.
    fn screen(&self) -> Idle<'_> {
        Idle {
            unit: &self.unit,
            at: self.at,
            preview: self.keys.is_some(),
        }
    }

    /// Fold one message in, at `at` seconds on the screen's clock. A `present`
    /// asks the harness for a new surface, and every other message reaches the
    /// unit.
    ///
    /// The retained status also asks, on its move to `Idle`. `present` rides a
    /// topic nothing retains, so a broker session that drops while a film ends
    /// loses the moment for good, and the catch-up status still says the
    /// screen is the client's again. The two usually drain in one wake, and
    /// the flag folds them into one map.
    pub fn receive(&mut self, message: Message, at: f64) {
        if message == Message::Screen(bus::screen::Event::Present) {
            self.surface_due = true;
        }
        let was = self.unit.activity;
        self.unit.fold(message, at);
        if self.unit.activity == Activity::Idle && was != Activity::Idle {
            self.surface_due = true;
        }
    }
}

impl Screen for Client {
    // Nothing on the screen emits a message yet, and the type says so.
    type Message = Infallible;

    fn background(&self) -> Color {
        look::BACKGROUND
    }

    /// One key press. A run with no preview keys bound takes none, so a
    /// keyboard attached to a pod changes nothing on a screen in a house.
    ///
    /// A bound key builds the messages the bus would carry and folds them
    /// through the call the bus reader's messages take, so the press exercises
    /// the handlers a cluster exercises.
    fn key(&mut self, name: &str) {
        let Some(keys) = &mut self.keys else {
            return;
        };
        let messages = keys.press(name);
        let at = self.at;
        for message in messages {
            self.receive(message, at);
        }
    }

    fn tick(&mut self, at: f64) {
        self.at = at;
    }

    /// Drain the reader and fold in what arrived. The harness calls this on
    /// every wake of the loop rather than on a frame, because a covered
    /// client draws no frame, and `present` arrives exactly while the client
    /// is covered.
    fn pump(&mut self, at: f64) -> bool {
        let Some(bus) = &self.bus else {
            return false;
        };
        let messages = bus.drain();
        let folded = !messages.is_empty();
        for message in messages {
            self.receive(message, at);
        }
        folded
    }

    fn surface_due(&mut self) -> bool {
        std::mem::take(&mut self.surface_due)
    }

    fn surfaced(&mut self, at: f64) {
        self.unit.presented = Some(at);
    }

    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer> {
        Canvas::new(self.screen())
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }

    /// The second the screen next changes. The elements answer it, and the
    /// bus does not: the reader drains in `pump`, which the harness calls on
    /// every wake of the loop. The clock's next second is what bounds each
    /// wait, so the broker is read at least once a second whatever else the
    /// screen does.
    fn next_frame(&self, at: f64) -> Option<f64> {
        self.screen().next_frame(at)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::bus::screen;
    use crate::bus::status::{Activity, Status};
    use crate::wiring::Topics;

    fn seeded() -> Client {
        Client::open(Wiring {
            player_name: "The Den".into(),
            components: vec!["The screen".into()],
            topics: Topics::default(),
            ..Wiring::default()
        })
    }

    #[test]
    fn the_client_draws_on_the_theme_ground() {
        assert_eq!(seeded().background(), look::BACKGROUND);
    }

    #[test]
    fn a_client_with_no_broker_draws_the_seeds() {
        let mut client = seeded();
        assert_eq!(client.unit().name, "The Den");
        assert_eq!(client.unit().parts.len(), 1);

        // The clock advances with no reader, and nothing changes.
        client.tick(1.0);
        assert_eq!(client.unit().name, "The Den");
    }

    #[test]
    fn a_settled_client_still_asks_for_a_frame_every_second() {
        let mut client = seeded();
        client.tick(7.25);

        // The clock's next second bounds every wait, so a client that named
        // no second would leave the loop with no timer to pump the bus on.
        assert_eq!(client.next_frame(7.25), Some(8.0));
    }

    #[test]
    fn the_pump_folds_what_the_bus_delivered_and_says_so() {
        let (sender, messages) = std::sync::mpsc::channel();
        let mut client = seeded();
        client.bus = Some(Reader::from_channel(messages));

        assert!(!client.pump(1.0));

        sender
            .send(Message::Screen(screen::Event::Present))
            .expect("the channel is open");

        assert!(client.pump(2.0));
        assert!(client.surface_due());
    }

    #[test]
    fn the_clock_moves_without_the_bus() {
        let mut client = seeded();
        client.tick(3.5);
        assert_eq!(client.next_frame(3.5), Some(4.0));
    }

    #[test]
    fn a_status_reaches_the_unit() {
        let mut client = seeded();
        client.receive(
            Message::Status(Status {
                activity: Activity::Playing,
                ..Status::default()
            }),
            2.0,
        );
        assert_eq!(client.unit().activity, Activity::Playing);
    }

    #[test]
    fn the_retained_status_heals_a_present_the_client_never_received() {
        let mut client = seeded();
        client.receive(
            Message::Status(Status {
                activity: Activity::Playing,
                ..Status::default()
            }),
            2.0,
        );
        assert!(!client.surface_due());

        client.receive(
            Message::Status(Status {
                activity: Activity::Idle,
                ..Status::default()
            }),
            9.0,
        );
        assert!(client.surface_due());
    }

    #[test]
    fn a_present_asks_for_one_new_surface() {
        let mut client = seeded();
        assert!(!client.surface_due());

        client.receive(Message::Screen(screen::Event::Present), 3.0);

        assert!(client.surface_due());
        assert!(!client.surface_due());
    }

    #[test]
    fn the_unit_takes_the_second_the_new_surface_went_up() {
        let mut client = seeded();
        client.receive(Message::Screen(screen::Event::Present), 3.0);
        assert_eq!(client.unit().presented, None);

        client.surfaced(3.2);

        assert_eq!(client.unit().presented, Some(3.2));
    }

    fn previewing() -> Client {
        Client::open(Wiring {
            player_name: "Studio Lab".into(),
            components: vec!["Portable Screen".into(), "Studio Dualsense".into()],
            topics: Topics::default(),
            preview: true,
            ..Wiring::default()
        })
    }

    #[test]
    fn a_run_with_no_preview_keys_takes_no_key() {
        let mut client = seeded();
        let before = client.unit().clone();

        client.key("p");

        assert_eq!(client.unit(), &before);
    }

    #[test]
    fn a_preview_key_reaches_the_unit_through_the_bus_path() {
        let mut client = previewing();
        client.tick(2.0);

        client.key("p");

        assert_eq!(client.unit().activity, Activity::Starting);
        assert_eq!(client.unit().title.as_deref(), Some("Sailing"));
    }

    #[test]
    fn the_film_end_key_returns_the_status_and_asks_for_a_new_surface() {
        let mut client = previewing();
        client.tick(4.0);
        client.key("o");

        client.key("i");

        assert_eq!(client.unit().activity, Activity::Idle);
        assert!(client.surface_due());
    }

    #[test]
    fn the_presence_key_disconnects_the_last_part() {
        let mut client = previewing();
        client.tick(1.0);

        client.key("d");

        assert_eq!(client.unit().parts[1].connected, Some(false));
    }

    #[test]
    fn the_focus_key_marks_the_remote_at_the_second_of_the_press() {
        let mut client = previewing();
        client.tick(5.0);

        client.key("f");
        assert!(client.unit().parts[1].focused);
        assert_eq!(client.unit().parts[1].marked, Some(5.0));

        client.key("g");
        assert!(!client.unit().parts[1].focused);
    }

    #[test]
    fn the_sleep_key_draws_the_shade_down_and_then_up() {
        let mut client = previewing();
        client.tick(6.0);

        client.key("s");
        assert_eq!(client.unit().shade.map(|shade| shade.down), Some(true));

        client.key("s");
        assert_eq!(client.unit().shade.map(|shade| shade.down), Some(false));
    }

    #[test]
    fn a_volume_key_shows_the_indicator_the_way_a_press_shows_it() {
        let mut client = previewing();
        client.tick(7.0);

        client.key("9");

        assert_eq!(client.unit().volume.level, 95);
        assert_eq!(client.unit().pressed, Some(7.0));

        client.key("m");
        assert!(client.unit().volume.muted);
    }
}
