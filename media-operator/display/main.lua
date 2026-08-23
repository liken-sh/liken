-- The frame loop. It drives one overlay for the whole display, so the modules
-- share one z order instead of two overlays racing on it.
local theme = require("theme")
local focus = require("focus")
local scrubber = require("scrubber")
local strip = require("strip")
local images = require("images")
local presentation = require("presentation")
local header = require("header")

-- The remote reaches this client by its directory basename. The log names it
-- once, so a wrong name shows in the player log.
mp.msg.info("liken display loaded as script " .. mp.get_script_name())

local overlay = mp.create_osd_overlay("ass-events")
overlay.res_x = theme.canvas.w
overlay.res_y = theme.canvas.h

-- Draw the scrubber, then the strip, then the open chooser last. The scrubber
-- owns two focus stops but draws one bar, told which axis is focused. The
-- chooser covers the two while it captures input, so it draws on top.
local function redraw()
  -- The logo overlay tracks the OSD. It shows while the OSD is up, and it hides
  -- when the OSD hides and while a chooser captures, so a corner logo never
  -- lingers over a plain frame or floats above the chooser's dim. overlay-add
  -- draws over the ASS layer, so a logo left in place would sit on top of the
  -- dim rather than under it.
  header.sync(focus.visible() and not focus.capturing())
  local parts = {}
  if focus.visible() then
    local h = header.draw()
    if h then
      -- The top scrim backs the header, drawn before it so the header text is
      -- on top.
      parts[#parts + 1] = theme.scrim(0, 0, theme.canvas.w, theme.scrim_top_h, "top")
      parts[#parts + 1] = h
    end
    local stop = focus.focused_stop()
    local axis = nil
    if stop == "fine" then
      axis = "fine"
    elseif stop == "chapter" then
      axis = "chapter"
    end
    local bottom = {}
    if presentation.type() ~= "image" then
      local s = scrubber.draw(axis)
      if s then
        bottom[#bottom + 1] = s
      end
    end
    if images.available() then
      local im = images.draw()
      if im then
        bottom[#bottom + 1] = im
      end
    end
    local t = strip.draw(stop == "strip")
    if t then
      bottom[#bottom + 1] = t
    end
    if #bottom > 0 then
      -- The bottom scrim backs the scrubber and the strip, drawn before them
      -- so their text is on top.
      parts[#parts + 1] = theme.scrim(
        0, theme.canvas.h - theme.scrim_bottom_h, theme.canvas.w, theme.scrim_bottom_h, "bottom"
      )
      for _, b in ipairs(bottom) do
        parts[#parts + 1] = b
      end
    end
    local capturing = focus.capturing()
    if capturing then
      -- A chooser is open. Dim the whole frame under it, so the list reads
      -- as the one thing in focus and the scrubber and strip recede.
      parts[#parts + 1] = theme.rect(0, 0, theme.canvas.w, theme.canvas.h, theme.color.shadow, theme.alpha.dim)
      parts[#parts + 1] = capturing.draw_chooser()
    end
  end
  overlay.data = table.concat(parts, "\n")
  overlay:update()
end

-- Batch every property change and every seek into one redraw per event-loop
-- pass, so a held seek does not thrash the overlay update.
local pending = false
local function request_redraw()
  if pending then
    return
  end
  pending = true
  mp.add_timeout(0, function()
    pending = false
    redraw()
  end)
end

focus.set_redraw(request_redraw)
scrubber.set_redraw(request_redraw)
strip.set_redraw(request_redraw)
images.set_redraw(request_redraw)
presentation.set_redraw(request_redraw)
header.set_redraw(request_redraw)

-- mpv pushes each property once when the script observes it, then on every
-- change, so the display runs no timer of its own for these values.
mp.observe_property("duration", "number", function()
  request_redraw()
end)
mp.observe_property("time-pos", "number", function()
  request_redraw()
end)
mp.observe_property("percent-pos", "number", function()
  request_redraw()
end)
mp.observe_property("chapter", "number", function()
  request_redraw()
end)
mp.observe_property("chapter-list", "native", function()
  request_redraw()
end)
mp.observe_property("track-list", "native", function()
  request_redraw()
end)
mp.observe_property("aid", "string", function()
  request_redraw()
end)
mp.observe_property("media-title", "string", function()
  request_redraw()
end)
mp.observe_property("sid", "string", function()
  request_redraw()
end)
mp.observe_property("playlist-pos", "number", function()
  request_redraw()
end)
mp.observe_property("playlist-count", "number", function()
  request_redraw()
end)
-- A screen resize changes the pixel size the logo needs, so the header asks
-- the bridge for the logo again at the new size.
mp.observe_property("osd-dimensions", "native", function()
  header.on_resize()
  request_redraw()
end)
mp.observe_property("pause", "bool", function(_, value)
  focus.on_pause(value == true)
end)

-- The command sidecar hands the current item's presentation block to the
-- display over this script-message, as one JSON string.
-- A new block is a new item, so the header swaps its logo with it.
mp.register_script_message("presentation", function(text)
  presentation.receive(text)
  header.on_item()
end)

-- The bridge answers a logo request over this script-message, carrying the
-- decoded bitmap the header places.
mp.register_script_message("liken-art", header.on_art)

-- The six navigation actions the command bus carries. A Keymap binds a
-- controller's buttons to them.
for _, action in ipairs({ "up", "down", "left", "right", "select", "back" }) do
  mp.register_script_message(action, function()
    focus.nav(action)
  end)
end
