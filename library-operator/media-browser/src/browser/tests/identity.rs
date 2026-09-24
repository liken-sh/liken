// The identity block the picker draws: the unit the last status on the
// bus named, and only while the picker is up.

use super::*;

// A status that names the unit and one controller, the way the operator
// publishes it once a remote is paired to the `Player`.
fn den() -> Moment {
    Moment::Status(Status {
        display_name: "The Den".into(),
        components: vec![media_screen::status::Component {
            name: "Remote".into(),
            kind: "remote".into(),
            connected: Some(true),
            battery: Some(72),
            focused: Some(true),
        }],
        ..Status::default()
    })
}

// The browser after the bus delivered that status and the first frame
// asked who is watching.
fn asked_in_the_den() -> Browser<Fake, NoArt> {
    let (browser, bus) = on_bus(3, Vec::new());
    let people = ["first", "second"]
        .map(|name| crate::audience::Person {
            name: name.to_string(),
            display_name: name.to_string(),
        })
        .to_vec();
    let mut browser = browser.with_audience(people, Vec::new());
    *bus.inbound.lock().expect("no test panics with the lock") = vec![den()];
    browser.pump(0.0);
    browser.tick(0.0);
    browser
}

#[test]
fn the_picker_draws_the_unit_the_last_status_named() {
    let browser = asked_in_the_den();
    let layer = browser.picker_layer().expect("the picker is up");
    let frame = views::area(0.0, 0.0, 1920.0, 1080.0);
    let block =
        screens::audience::identity::block(layer.unit, frame).expect("the status named the unit");

    assert_eq!(block.header.words, "The Den");
    assert_eq!(block.lines.len(), 1);
    assert_eq!(block.lines[0].run.words, "Remote");
    assert!(block.lines[0].bar.is_some());
    assert!(block.lines[0].marker.is_some());
}

#[test]
fn the_block_leaves_with_the_picker() {
    let mut browser = asked_in_the_den();

    browser.key("escape");

    assert!(browser.picker.is_none());
    assert!(browser.picker_layer().is_none());
}
