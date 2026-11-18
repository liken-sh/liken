use media_display::ipc::Ipc;
use media_display::window;

fn main() {
    let ipc = Ipc::from_environment();

    // This runtime is dropped before the window opens, because the GUI
    // toolkit starts its own runtime.
    let waiting = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("building the runtime that waits for mpv");
    let playing = waiting.block_on(ipc.playing(window::PLAYER_GRACE));
    drop(waiting);

    if !playing {
        eprintln!(
            "media-display: mpv reported no position within {:?}",
            window::PLAYER_GRACE
        );
        std::process::exit(1);
    }

    if let Err(error) = window::run(ipc) {
        eprintln!("media-display: {error}");
        std::process::exit(1);
    }
}
