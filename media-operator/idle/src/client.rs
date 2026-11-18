// The client that the idle pod runs. It reads the state of the unit from the
// message bus, keeps it in a `Unit`, and draws the idle screen from it.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::Canvas;
use iced_winit::core::{Color, Element, Length, Theme};

use media_screen::status::Activity;
use media_screen::{Bus, Moment, Reader, reader};

use crate::harness::Screen;
use crate::idle::Idle;
use crate::idle::preview::Keys;
use crate::look;
use crate::unit::{Arrival, Unit};
use crate::wiring::Wiring;

/// The client identifier prefix for the broker connection. The hostname is
/// appended to it. A broker closes the older connection when two arrive
/// under one identifier, so no two clients on one machine may share it.
///
/// It is also the `component` label of this process on `liken_build_info`.
pub const CLIENT: &str = "idle-screen";

#[derive(Debug)]
pub struct Client {
    unit: Unit,
    /// `None` when the wiring names no broker, as on a workstation run. The
    /// screen then shows only the names from the wiring. It is a `Bus` trait
    /// object so that a test can replace the `Reader` with a channel.
    bus: Option<Box<dyn Bus>>,
    /// Set when the wiring turns on preview. Each preview key sends the
    /// messages that the bus would send, and the screen draws a legend of
    /// the keys.
    keys: Option<Keys>,
    /// The second of the last `tick`, on the screen's clock.
    at: f64,
    /// Reads how far the wall clock is into its current minute. A test
    /// replaces it to set the wall clock.
    wall: fn() -> f64,
    /// What `wall` returned at the last `tick`. The harness calls
    /// `next_frame` after the frame's work is done. Reading `wall` then
    /// would schedule the minute turn early by the length of that work, and
    /// the frame drawn at that second would still show the old minute.
    into_minute: f64,
}

impl Client {
    /// The unit starts with the player and component names from `wiring`, so
    /// the first frame, drawn before the broker sends anything, is not blank.
    pub fn open(wiring: Wiring) -> Self {
        let client_id = reader::client_id(CLIENT, &reader::hostname());
        let keys = wiring.preview.then(|| {
            Keys::before_first_status(wiring.player_name.clone(), wiring.components.clone())
        });
        // The idle screen subscribes to no topic of its own.
        let bus = Reader::open(&wiring.screen, &client_id, &[]);
        Self {
            unit: Unit::before_first_status(wiring.player_name, wiring.components),
            bus: bus.map(|reader| Box::new(reader) as Box<dyn Bus>),
            keys,
            at: 0.0,
            wall: crate::clock::into_minute,
            into_minute: 0.0,
        }
    }

    pub fn unit(&self) -> &Unit {
        &self.unit
    }

    /// The screen as of the last `tick`. `view` and `next_frame` both build
    /// it here, so the drawing and the schedule use the same values.
    fn screen(&self) -> Idle<'_> {
        Idle {
            unit: &self.unit,
            at: self.at,
            preview: self.keys.is_some(),
            into_minute: self.into_minute,
        }
    }

    /// Applies one message to the unit at `at` seconds on the screen's clock.
    ///
    /// When a status changes the activity to `Idle`, the `Play` has ended,
    /// so `receive` records an `Arrival` at that second. Only a change
    /// records one, because a later status that keeps `Idle` and changes
    /// another field must not restart the arrival. The arrival records the
    /// activity the unit left, because the energy of the mark depends on
    /// it: an arrival from `Playing` starts at full energy, and an arrival
    /// from `Starting` starts at the energy that the mark already had.
    pub fn receive(&mut self, moment: Moment, at: f64) {
        // `media-screen` passes this client every key except its own and the
        // power keys. This screen has no list to move through, so it acts
        // only on back, and asks the bus to dim the screen. The client
        // decides this and not `media-screen`, because a client with nested
        // screens dims only when back is pressed at its top screen.
        if let Moment::Press(key) = &moment
            && media_screen::screen::keys::back(key)
            && let Some(bus) = &self.bus
        {
            bus.sleep();
        }
        let was = self.unit.activity;
        self.unit.fold(moment, at);
        if self.unit.activity == Activity::Idle && was != Activity::Idle {
            self.unit.arrived = Some(Arrival { at, from: was });
        }
    }
}

impl Screen for Client {
    // The screen produces no messages of its own.
    type Message = Infallible;

    fn background(&self) -> Color {
        look::BACKGROUND
    }

    /// Without preview keys this does nothing, so a keyboard plugged into
    /// the machine changes nothing on the screen. A preview key's messages
    /// go through `receive`, the same path that bus messages take.
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
        self.into_minute = (self.wall)();
    }

    /// Gives the event loop's waker to the reader, so a controller press is
    /// drawn on the next frame and not at the harness's next timed wake.
    fn wake_by(&mut self, wake: media_screen::Waker) {
        if let Some(bus) = &self.bus {
            bus.wake_on_delivery(wake);
        }
    }

    /// Applies the messages that arrived since the last call and returns
    /// whether there were any. The harness calls this on every wake of the
    /// loop and not only in a frame, because while a film plays its surface
    /// covers this screen, no frames are drawn, and the status that ends the
    /// film arrives then.
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

    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer> {
        Canvas::new(self.screen())
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }

    /// The second at which an animation or the clock next changes the
    /// screen. Bus messages are not part of this schedule: each delivery
    /// wakes the loop through the waker. While a frame is scheduled, the
    /// harness also wakes at least once a second to call `pump`, in case a
    /// wake is lost. With nothing
    /// animating, the next frame is at the next minute turn.
    fn next_frame(&self, at: f64) -> Option<f64> {
        self.screen().next_frame(at)
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::mpsc;

    use super::*;
    use crate::idle::{activity, energy};
    use media_screen::status::{Activity, Status};

    /// A `Bus` over a channel, so a test can send moments and count dimming
    /// requests with no broker. The count is shared because the client owns
    /// the bus.
    #[derive(Debug)]
    struct Channel {
        moments: mpsc::Receiver<Moment>,
        slept: Arc<AtomicUsize>,
    }

    impl Bus for Channel {
        fn drain(&self) -> Vec<Moment> {
            self.moments.try_iter().collect()
        }

        fn sleep(&self) {
            self.slept.fetch_add(1, Ordering::SeqCst);
        }

        fn publish(&self, _topic: &str, _payload: Vec<u8>, _retained: bool) {}

        fn wake_on_delivery(&self, _wake: media_screen::Waker) {}
    }

    /// Replaces the bus of `client` with a `Channel`, and returns the sender
    /// for moments and the count of dimming requests.
    fn on_a_channel(client: &mut Client) -> (mpsc::Sender<Moment>, Arc<AtomicUsize>) {
        let (sender, moments) = mpsc::channel();
        let slept = Arc::new(AtomicUsize::new(0));
        client.bus = Some(Box::new(Channel {
            moments,
            slept: Arc::clone(&slept),
        }));
        (sender, slept)
    }

    fn before_first_status() -> Client {
        Client::open(Wiring {
            player_name: "The Den".into(),
            components: vec!["The screen".into()],
            ..Wiring::default()
        })
    }

    #[test]
    fn the_client_draws_on_the_theme_background() {
        assert_eq!(before_first_status().background(), look::BACKGROUND);
    }

    #[test]
    fn a_client_with_no_broker_draws_the_names_from_the_wiring() {
        let mut client = before_first_status();
        assert_eq!(client.unit().name, "The Den");
        assert_eq!(client.unit().parts.len(), 1);

        client.tick(1.0);
        assert_eq!(client.unit().name, "The Den");
    }

    #[test]
    fn a_client_with_no_animation_schedules_a_frame_for_the_next_minute_turn() {
        let mut client = before_first_status();
        client.wall = || 50.0;
        client.tick(7.25);

        // The wall clock is 50 seconds into its minute, so the minute turns
        // 10 seconds later.
        assert_eq!(client.next_frame(7.25), Some(17.25));
    }

    // The wall clock moves on between the tick and `next_frame`, and the
    // frame for the new minute must still be due at the turn.
    #[test]
    fn the_minute_turn_is_measured_from_the_second_of_the_frame() {
        let mut client = before_first_status();
        client.wall = || 50.0;
        client.tick(7.25);
        client.wall = || 50.02;

        assert_eq!(client.next_frame(7.25), Some(17.25));
    }

    // A level message that a volume key press caused.
    fn pressed() -> Moment {
        Moment::Level {
            volume: media_screen::volume::Volume {
                level: 0.4,
                ..Default::default()
            },
            pressed: true,
        }
    }

    // A volume change at second 10: the row fades in until 10.35, stays until
    // 14.0, and fades out until 14.6.
    fn a_press() -> Vec<(Moment, f64)> {
        vec![(pressed(), 10.0)]
    }

    // A `Sleep` at second 10: the screen dims until it is dark at 14.0.
    fn a_sleep() -> Vec<(Moment, f64)> {
        vec![(Moment::Sleep, 10.0)]
    }

    // A `Sleep` at second 6 and a `Wake` at 10: the screen brightens until it
    // is clear at 10.4.
    fn a_wake() -> Vec<(Moment, f64)> {
        vec![(Moment::Sleep, 6.0), (Moment::Wake, 10.0)]
    }

    // A frame drawn 0.01 seconds before an animation ends is due again at
    // once. A frame at or after the end is next due at the start of the
    // volume row's fade out or at the next minute turn.
    #[test]
    fn a_frame_drawn_just_before_an_animation_ends_is_due_again() {
        let cases = [
            (a_press(), 10.34, Some(10.34)),
            (a_press(), 10.35, Some(14.0)),
            (a_press(), 14.59, Some(14.59)),
            (a_press(), 14.75, Some(54.75)),
            (a_sleep(), 13.99, Some(13.99)),
            (a_sleep(), 14.0, Some(54.0)),
            (a_wake(), 10.39, Some(10.39)),
            (a_wake(), 10.5, Some(50.5)),
        ];
        for (moments, drawn, due) in cases {
            let mut client = before_first_status();
            client.wall = || 20.0;
            for (moment, at) in moments {
                client.receive(moment, at);
            }
            client.tick(drawn);
            assert_eq!(client.next_frame(drawn), due, "{drawn}");
        }
    }

    #[test]
    fn the_pump_applies_bus_messages_and_reports_whether_any_arrived() {
        let mut client = before_first_status();
        let (sender, _slept) = on_a_channel(&mut client);

        assert!(!client.pump(1.0));

        sender
            .send(Moment::Status(Status {
                activity: Activity::Playing,
                ..Status::default()
            }))
            .expect("the channel is open");

        assert!(client.pump(2.0));
        assert_eq!(client.unit().activity, Activity::Playing);
    }

    #[test]
    fn a_back_press_asks_the_bus_to_dim_the_screen() {
        let mut client = before_first_status();
        let (_sender, slept) = on_a_channel(&mut client);

        for key in media_screen::screen::keys::BACK {
            client.receive(Moment::Press(key.into()), 1.0);
        }
        // The arrow and select keys do nothing.
        client.receive(Moment::Press("KEY_UP".into()), 2.0);
        client.receive(Moment::Press("KEY_ENTER".into()), 2.0);

        assert_eq!(
            slept.load(Ordering::SeqCst),
            media_screen::screen::keys::BACK.len()
        );
    }

    #[test]
    fn a_client_with_no_bus_ignores_a_back_press() {
        let mut client = before_first_status();

        client.receive(Moment::Press("KEY_BACK".into()), 1.0);

        assert!(client.bus.is_none());
    }

    #[test]
    fn the_clock_moves_without_the_bus() {
        let mut client = before_first_status();
        client.wall = || 59.0;
        client.tick(3.5);
        assert_eq!(client.next_frame(3.5), Some(4.5));
    }

    #[test]
    fn a_status_reaches_the_unit() {
        let mut client = before_first_status();
        client.receive(
            Moment::Status(Status {
                activity: Activity::Playing,
                ..Status::default()
            }),
            2.0,
        );
        assert_eq!(client.unit().activity, Activity::Playing);
    }

    #[test]
    fn a_status_change_to_idle_records_the_arrival() {
        let mut client = before_first_status();
        client.receive(
            Moment::Status(Status {
                activity: Activity::Playing,
                ..Status::default()
            }),
            2.0,
        );
        assert_eq!(client.unit().arrived, None);

        client.receive(
            Moment::Status(Status {
                activity: Activity::Idle,
                ..Status::default()
            }),
            9.0,
        );
        assert_eq!(
            client.unit().arrived,
            Some(Arrival {
                at: 9.0,
                from: Activity::Playing
            })
        );
    }

    /// The second at which the table below applies its first status.
    const FIRST: f64 = 1.0;

    /// A second status at `SETTLED` arrives after the 1200 ms ramp up has
    /// ended, so the energy has reached the level that the first status set.
    const SETTLED: f64 = 5.0;

    /// A second status at `MID_RAMP` arrives halfway up the ramp, as when a
    /// `Play` fails early.
    const MID_RAMP: f64 = 1.6;

    /// Compares two energy values with the tolerance that the tests of
    /// `energy` use.
    #[track_caller]
    fn assert_close(measured: f64, expected: f64, row: &str) {
        assert!(
            (measured - expected).abs() < 1e-9,
            "the energy on {row} is {measured}, want {expected}"
        );
    }

    /// A status message that carries only `activity`.
    fn saying(activity: Activity) -> Moment {
        Moment::Status(Status {
            activity,
            ..Status::default()
        })
    }

    /// A client that applied one status at [`FIRST`] and a second at `at`,
    /// both through `receive`, because `receive` records the arrival.
    fn moved(from: Activity, to: Activity, at: f64) -> Client {
        let mut client = before_first_status();
        client.receive(saying(from), FIRST);
        client.receive(saying(to), at);
        client
    }

    /// The mark starts moving at `Starting`, before the film starts, and
    /// stops at `Playing`, because the film's surface covers this screen. A
    /// change to `Idle` records the arrival, and the mark's energy and the
    /// activity line then fall together over the ramp down. A status that
    /// repeats the current activity changes neither the arrival nor the
    /// ramp.
    ///
    /// The last two rows are the same change at two different seconds, and
    /// show why the arrival records the activity it left. An arrival from
    /// `Playing` starts at full energy, and an arrival from `Starting` keeps
    /// the energy the mark had: 0.5 for a `Play` that ends halfway up the
    /// ramp. The line stays fully opaque in that frame.
    #[test]
    fn each_activity_change_sets_the_arrival_the_ramp_and_the_line() {
        use Activity::{Idle, Playing, Starting};

        // from, to, the second of the change, the activity recorded in the
        // arrival, whether the ramp restarts, the energy, the line's opacity
        let table = [
            (Idle, Starting, SETTLED, None, true, 0.0, 1.0),
            (Starting, Playing, SETTLED, None, true, 0.0, 1.0),
            (Playing, Idle, SETTLED, Some(Playing), true, 1.0, 1.0),
            (Starting, Starting, SETTLED, None, false, 1.0, 1.0),
            (Idle, Idle, SETTLED, None, false, 0.0, 0.0),
            (Starting, Idle, SETTLED, Some(Starting), true, 1.0, 1.0),
            (Starting, Idle, MID_RAMP, Some(Starting), true, 0.5, 1.0),
        ];

        for (from, to, at, arrived, ramped, energy, alpha) in table {
            let client = moved(from, to, at);
            let unit = client.unit();
            let row = format!("{from:?} to {to:?} at {at}");

            let mark = arrived.map(|from| Arrival { at, from });
            assert_eq!(unit.arrived, mark, "the arrival on {row}");
            assert_eq!(unit.ramp.since == at, ramped, "the ramp on {row}");
            assert_close(energy::level(unit, at), energy, &row);
            assert_eq!(activity::opacity(unit, at), alpha, "the line on {row}");
        }
    }

    fn previewing() -> Client {
        Client::open(Wiring {
            player_name: "Studio Lab".into(),
            components: vec!["Portable Screen".into(), "Studio Dualsense".into()],
            preview: true,
            ..Wiring::default()
        })
    }

    #[test]
    fn a_run_with_no_preview_keys_ignores_key_presses() {
        let mut client = before_first_status();
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
    fn the_film_end_key_returns_the_status_and_marks_the_arrival() {
        let mut client = previewing();
        client.tick(4.0);
        client.key("o");

        client.key("i");

        assert_eq!(client.unit().activity, Activity::Idle);
        assert_eq!(
            client.unit().arrived,
            Some(Arrival {
                at: 4.0,
                from: Activity::Playing
            })
        );
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
    fn the_sleep_key_dims_the_screen_and_then_restores_it() {
        let mut client = previewing();
        client.tick(6.0);

        client.key("s");
        assert_eq!(client.unit().shade.map(|shade| shade.down), Some(true));

        client.key("s");
        assert_eq!(client.unit().shade.map(|shade| shade.down), Some(false));
    }

    #[test]
    fn a_volume_key_shows_the_volume_row_like_a_press() {
        let mut client = previewing();
        client.tick(7.0);

        client.key("9");

        assert_eq!(client.unit().volume.percent(), 95);
        assert_eq!(client.unit().pressed, Some(7.0));

        client.key("m");
        assert!(client.unit().volume.muted);
    }
}
