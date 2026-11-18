use std::sync::Arc;

use iced_wgpu::graphics::Shell;
use iced_wgpu::{Engine, Renderer, wgpu};
use iced_winit::core::{Font, Pixels};
use iced_winit::winit;

use winit::event_loop::ActiveEventLoop;

/// The window and the wgpu state built on it. It exists only after the
/// compositor has created the window.
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

/// Creates the window, or returns `None` when the compositor refuses.
///
/// The window sets no Wayland app-id, because the Wayland socket of the
/// display claim already selects the screen and the compositor's layout
/// module does not read an app-id.
pub fn window(
    event_loop: &ActiveEventLoop,
    size: (u32, u32),
) -> Option<Arc<winit::window::Window>> {
    let attributes = winit::window::WindowAttributes::default()
        .with_title("liken idle screen")
        // Without this, winit's Wayland backend draws a title bar that
        // takes 35 rows off the surface.
        .with_decorations(false)
        .with_inner_size(winit::dpi::PhysicalSize::new(size.0, size.1));

    match event_loop.create_window(attributes) {
        Ok(window) => Some(Arc::new(window)),
        Err(error) => {
            eprintln!("idle-screen: creating the window: {error}");
            None
        }
    }
}

/// Opens the window, selects a wgpu adapter, and builds the renderer.
///
/// Every failure returns `None`, the same as a refused window. The
/// harness then keeps waiting and the watchdog keeps counting, so the
/// pod exits with the watchdog's code whichever step failed.
pub fn open(event_loop: &ActiveEventLoop, size: (u32, u32)) -> Option<Graphics> {
    let window = window(event_loop, size)?;

    let physical = window.inner_size();
    let instance = wgpu::Instance::new(&wgpu::InstanceDescriptor {
        backends: wgpu::Backends::from_env().unwrap_or_default(),
        ..Default::default()
    });
    let surface = match instance.create_surface(window.clone()) {
        Ok(surface) => surface,
        Err(error) => {
            eprintln!("idle-screen: creating the wgpu surface for the window: {error}");
            return None;
        }
    };

    let (format, adapter, device, queue) = block_on(async {
        let adapter =
            match wgpu::util::initialize_adapter_from_env_or_default(&instance, Some(&surface))
                .await
            {
                Ok(adapter) => adapter,
                Err(error) => {
                    eprintln!("idle-screen: no wgpu adapter can draw to the surface: {error}");
                    return None;
                }
            };

        let capabilities = surface.get_capabilities(&adapter);
        let (device, queue) = match adapter
            .request_device(&wgpu::DeviceDescriptor {
                label: None,
                required_features: adapter.features() & wgpu::Features::default(),
                required_limits: wgpu::Limits::default(),
                memory_hints: wgpu::MemoryHints::MemoryUsage,
                trace: wgpu::Trace::Off,
                experimental_features: wgpu::ExperimentalFeatures::disabled(),
            })
            .await
        {
            Ok(pair) => pair,
            Err(error) => {
                eprintln!("idle-screen: requesting a device from the wgpu adapter: {error}");
                return None;
            }
        };

        // The brand's colors and alphas are sRGB-encoded values, tuned by
        // eye with blending on those encoded values. An sRGB surface
        // format converts on every read and write, so it blends in linear
        // light and draws every fade too bright. So prefer a format that
        // stores the shader's output unchanged. The `web-colors` feature
        // of `iced_wgpu` makes the shader write encoded colors, and the
        // compositor reads the buffer as sRGB.
        //
        // Only the 8-bit formats qualify. A wider format such as
        // `Rgba16Unorm` needs a wgpu feature that this client does not
        // enable. When neither is offered, sRGB is the fallback.
        let encoded = |format: &wgpu::TextureFormat| {
            matches!(
                format,
                wgpu::TextureFormat::Bgra8Unorm | wgpu::TextureFormat::Rgba8Unorm
            )
        };
        let formats = || capabilities.formats.iter().copied();
        let format = formats()
            .find(encoded)
            .or_else(|| formats().find(wgpu::TextureFormat::is_srgb))
            .or_else(|| formats().next());
        let Some(format) = format else {
            eprintln!("idle-screen: the surface supports no texture format");
            return None;
        };

        Some((format, adapter, device, queue))
    })?;

    // cage takes the next free `wayland-N` socket name, so the name is
    // not `wayland-1` when another compositor already has it. This line
    // puts the name the client used in the log.
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

/// Configures the surface for a window size, at start and on every
/// resize.
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
            // Not FIFO. While a film covers the screen, the Wayland
            // surface is hidden and the compositor releases no buffers.
            // FIFO then waits in mesa's poll for a release, and the
            // whole loop stops, the message bus with it. The loop's own
            // pacing caps the frame rate at one frame per `frame::STEP`.
            present_mode: wgpu::PresentMode::AutoNoVsync,
            alpha_mode: wgpu::CompositeAlphaMode::Auto,
            view_formats: vec![],
            desired_maximum_frame_latency: 2,
        },
    );
}

/// Runs one future to completion on this thread, parking the thread
/// until the waker unparks it. Only the wgpu setup is asynchronous, and
/// the `futures` executor is behind a feature that iced does not enable.
fn block_on<F: Future>(future: F) -> F::Output {
    struct Unpark(std::thread::Thread);

    impl std::task::Wake for Unpark {
        fn wake(self: Arc<Self>) {
            self.0.unpark();
        }
    }

    let waker = std::task::Waker::from(Arc::new(Unpark(std::thread::current())));
    let mut context = std::task::Context::from_waker(&waker);
    let mut future = std::pin::pin!(future);

    loop {
        match future.as_mut().poll(&mut context) {
            std::task::Poll::Ready(output) => return output,
            std::task::Poll::Pending => std::thread::park(),
        }
    }
}
