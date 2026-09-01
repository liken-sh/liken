// The window and the graphics device the frame loop draws through, kept apart
// from the loop itself.

use std::sync::Arc;

use iced_wgpu::graphics::Shell;
use iced_wgpu::{Engine, Renderer, wgpu};
use iced_winit::core::{Font, Pixels};
use iced_winit::winit;

use winit::event_loop::ActiveEventLoop;
use winit::platform::wayland::WindowAttributesExtWayland;

/// Everything that exists only after the compositor gives the process a window.
pub struct Graphics {
    pub window: Arc<winit::window::Window>,
    pub device: wgpu::Device,
    pub surface: wgpu::Surface<'static>,
    pub format: wgpu::TextureFormat,
    pub renderer: Renderer,
    pub backend: String,
    pub adapter: String,
    pub size: (u32, u32),
}

/// Ask the compositor for a window. The answer is `None` when it gave
/// none, and the watchdog reads that as a client with nothing to draw on.
///
/// `app_id` is the Wayland app-id the display claim delivered, and the
/// compositor places the window on the claimed screen by it. An empty id asks
/// for none, which is a run on a workstation where no claim named one.
pub fn window(
    event_loop: &ActiveEventLoop,
    size: (u32, u32),
    app_id: &str,
) -> Option<Arc<winit::window::Window>> {
    let mut attributes = winit::window::WindowAttributes::default()
        .with_title("liken media browser")
        // A kiosk client draws no title bar. winit's Wayland
        // backend draws one otherwise, and it takes 35 rows off
        // the surface the compositor gave the window.
        .with_decorations(false)
        .with_inner_size(winit::dpi::PhysicalSize::new(size.0, size.1));

    if !app_id.is_empty() {
        // The general name is the Wayland app-id. The instance name is
        // the second half of the same protocol field, and the compositor reads
        // neither of the two for anything this client needs.
        attributes = attributes.with_name(app_id, "");
    }

    match event_loop.create_window(attributes) {
        Ok(window) => Some(Arc::new(window)),
        Err(error) => {
            eprintln!("media-browser: the compositor gave no window: {error}");
            None
        }
    }
}

/// Open the window, pick an adapter, and build the renderer that draws into it.
///
/// a compositor that gives no window answers `None` here, so the run
/// leaves the watchdog counting and the kubelet reads the exit code the
/// watchdog states.
pub fn open(event_loop: &ActiveEventLoop, size: (u32, u32), app_id: &str) -> Option<Graphics> {
    let window = window(event_loop, size, app_id)?;

    let physical = window.inner_size();
    let instance = wgpu::Instance::new(&wgpu::InstanceDescriptor {
        backends: wgpu::Backends::from_env().unwrap_or_default(),
        ..Default::default()
    });
    let surface = instance
        .create_surface(window.clone())
        .expect("create surface");

    let (format, adapter, device, queue) = block_on(async {
        let adapter = wgpu::util::initialize_adapter_from_env_or_default(&instance, Some(&surface))
            .await
            .expect("no wgpu adapter for this surface");

        let capabilities = surface.get_capabilities(&adapter);
        let (device, queue) = adapter
            .request_device(&wgpu::DeviceDescriptor {
                label: None,
                required_features: adapter.features() & wgpu::Features::default(),
                required_limits: wgpu::Limits::default(),
                memory_hints: wgpu::MemoryHints::MemoryUsage,
                trace: wgpu::Trace::Off,
                experimental_features: wgpu::ExperimentalFeatures::disabled(),
            })
            .await
            .expect("request device");

        let format = capabilities
            .formats
            .iter()
            .copied()
            .find(wgpu::TextureFormat::is_srgb)
            .or_else(|| capabilities.formats.first().copied())
            .expect("no surface format");

        (format, adapter, device, queue)
    });

    // Cage takes the next free `wayland-N`, which is not `wayland-1` when
    // another compositor already holds that name, so the local script reads the
    // name out of this line rather than guessing it.
    println!(
        "wayland: {}",
        std::env::var("WAYLAND_DISPLAY").unwrap_or_else(|_| "none".into())
    );

    let info = adapter.get_info();
    eprintln!(
        "wgpu: {:?} on {} ({:?}), surface {:?} at {}x{}",
        info.backend, info.name, info.device_type, format, physical.width, physical.height
    );

    configure(
        &surface,
        &device,
        format,
        physical.width.max(1),
        physical.height.max(1),
    );

    let engine = Engine::new(
        &adapter,
        device.clone(),
        queue,
        format,
        None,
        Shell::headless(),
    );

    Some(Graphics {
        window,
        device,
        surface,
        format,
        renderer: Renderer::new(engine, Font::default(), Pixels::from(16)),
        backend: format!("{:?}", info.backend),
        adapter: info.name.clone(),
        size: (physical.width.max(1), physical.height.max(1)),
    })
}

/// Point the swapchain at a size. Every resize runs through here.
pub fn configure(
    surface: &wgpu::Surface<'static>,
    device: &wgpu::Device,
    format: wgpu::TextureFormat,
    width: u32,
    height: u32,
) {
    surface.configure(
        device,
        &wgpu::SurfaceConfiguration {
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
            format,
            width,
            height,
            // Mailbox rather than FIFO, because of what acquire does on a
            // hidden Wayland surface: FIFO waits in mesa's poll for a buffer
            // release the compositor never sends while a film covers the
            // screen, and the whole loop stops with it, bus and all. Mailbox
            // keeps spare images, so acquire never waits on the compositor.
            // The loop's own pace is what caps the rate instead: it never
            // asks for frames faster than `frame::STEP`.
            present_mode: wgpu::PresentMode::AutoNoVsync,
            alpha_mode: wgpu::CompositeAlphaMode::Auto,
            view_formats: vec![],
            desired_maximum_frame_latency: 2,
        },
    );
}

/// Wait for one future on this thread. Only wgpu's setup is asynchronous here,
/// and the futures crate ships its executor behind a feature iced does not
/// enable, so the harness parks the thread and lets the waker unpark it.
fn block_on<F: Future>(future: F) -> F::Output {
    struct Unpark(std::thread::Thread);

    impl std::task::Wake for Unpark {
        fn wake(self: Arc<Self>) {
            self.0.unpark();
        }
    }

    let waker = std::task::Waker::from(Arc::new(Unpark(std::thread::current())));
    let mut context = std::task::Context::from_waker(&waker);
    // The future never moves after this point, and it lives on this stack
    // frame until it resolves, so pinning it here is sound.
    let mut future = std::pin::pin!(future);

    loop {
        match future.as_mut().poll(&mut context) {
            std::task::Poll::Ready(output) => return output,
            std::task::Poll::Pending => std::thread::park(),
        }
    }
}
