// The binary waits for mpv, then opens the window. The compositor's
// controller stacks a claim's surfaces newest on top, so the display's
// surface has to arrive after the film's.

use media_osd::ipc::Ipc;
use media_osd::window;

fn main() {
    let ipc = Ipc::from_environment();

    // The wait runs on a runtime of its own that ends before the window
    // opens. The toolkit starts its own runtime for the window, and this
    // one exists only to hold the gate.
    let waiting = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("a runtime for the gate");
    let playing = waiting.block_on(ipc.playing(window::PLAYER_GRACE));
    drop(waiting);

    if !playing {
        eprintln!(
            "media-osd: mpv reported no position within {:?}",
            window::PLAYER_GRACE
        );
        std::process::exit(1);
    }

    if let Err(error) = window::run(ipc) {
        eprintln!("media-osd: {error}");
        std::process::exit(1);
    }
}
