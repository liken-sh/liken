-- The trickplay thumbnail on the scrub cursor. It shows a precomputed sprite
-- tile at the target time while the fine seek stop has focus. It asks the bridge
-- to crop and scale a tile, and places the returned bitmap with overlay-add
-- above the seekbar, the way the header places the logo.
local theme = require("theme")

local trickplay = {}

-- The tile's box in canvas coordinates. The bridge fits the tile inside the box
-- and keeps the aspect, so the returned bitmap may be smaller.
local TILE_W = 360
local TILE_H = 220

-- The tile's bottom edge in canvas coordinates. The tile sits above the time
-- label and the bar, and grows upward from this line.
local TILE_BOTTOM_Y = 836

-- The overlay id the thumbnail owns. The logo owns id 1, so the two bitmaps
-- keep separate ids in overlay-add's own numbering.
local OVERLAY_ID = 2

-- The two script-message names the display and the bridge agree on.
local ART_REQUEST = "liken-art-request"

local redraw_cb = function() end
function trickplay.set_redraw(fn)
  redraw_cb = fn
end

-- The decoded tile the bridge returned, nil until the bridge answers. The
-- display holds the last tile until a new one arrives, so it does not blink
-- during a scrub.
local blob = nil
-- The request last sent, the target second and the pixel box. An unchanged
-- second and box are not asked for again.
local want = nil
-- The overlay on screen now, so a redraw places it again only when the bitmap
-- or its position changes.
local placed = nil

-- Read how the canvas maps to the real screen, the way the header does. sx maps
-- a canvas x to real pixels, sy maps a canvas y, and overlay-add places in real
-- pixels, so the tile must use both to sit where the ass playhead draws. w
-- clamps the tile on screen.
local function osd_metrics()
  return theme.osd_scale()
end

-- Ask the bridge for the tile at the target time and pixel box. Quantize the
-- request to a whole second, so a scrub within one second sends nothing and the
-- IPC does not flood. A new second, or a new box, asks again.
local function request(time_seconds, w, h)
  local key = math.floor(time_seconds + 0.5) .. ":" .. w .. "x" .. h
  if key == want then
    return
  end
  want = key
  local ms = math.floor(time_seconds * 1000 + 0.5)
  mp.command_native({ "script-message", ART_REQUEST, "trickplay", tostring(ms), tostring(w), tostring(h) })
end

-- Place the tile centered on the playhead x, above the bar, in real pixels.
-- Clamp it, so it stays on screen at both ends.
local function place(m, x_canvas)
  local cx = math.floor(x_canvas * m.sx + 0.5)
  local x = cx - math.floor(blob.w / 2)
  if x + blob.w > m.w then
    x = m.w - blob.w
  end
  if x < 0 then
    x = 0
  end
  local y = math.floor(TILE_BOTTOM_Y * m.sy + 0.5) - blob.h
  if y < 0 then
    y = 0
  end
  local sig = table.concat({ blob.path, x, y, blob.w, blob.h, blob.stride }, ":")
  if sig ~= placed then
    mp.command_native({ "overlay-add", OVERLAY_ID, x, y, blob.path, 0, "bgra", blob.w, blob.h, blob.stride })
    placed = sig
  end
end

-- Remove the tile overlay when it is on screen.
local function clear()
  if placed then
    mp.command_native({ "overlay-remove", OVERLAY_ID })
    placed = nil
  end
end

-- On a new item, drop the tile and the pending request. A stale reply for the
-- old item then does not land on the new one.
function trickplay.on_item()
  blob = nil
  want = nil
  clear()
  redraw_cb()
end

-- On a resize, forget the last box, so the next sync asks at the new scale. The
-- current tile stays on screen until the new one arrives.
function trickplay.on_resize()
  want = nil
end

-- Receive one decoded tile from the bridge. Accept only a trickplay reply, so a
-- logo reply routed here by mistake draws nothing.
function trickplay.on_art(kind, path, w, h, stride)
  if kind ~= "trickplay" then
    return
  end
  blob = { path = path, w = tonumber(w), h = tonumber(h), stride = tonumber(stride) }
  redraw_cb()
end

-- sync places or removes the tile to match the display state. When it is
-- visible, it asks for the tile at the target time and places the tile it
-- holds. When it is not, it removes the overlay.
function trickplay.sync(visible, time_seconds, x_canvas)
  if not visible or not time_seconds or not x_canvas then
    clear()
    return
  end
  local m = osd_metrics()
  if not m then
    return
  end
  local w = math.floor(TILE_W * m.sx + 0.5)
  local h = math.floor(TILE_H * m.sy + 0.5)
  if w <= 0 or h <= 0 then
    return
  end
  request(time_seconds, w, h)
  if blob then
    place(m, x_canvas)
  end
end

return trickplay
