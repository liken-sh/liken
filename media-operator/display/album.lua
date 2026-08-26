-- The playing track's cover art, the one picture a music screen carries.
-- mpv decodes no video for music, so the cover stands on the black frame and
-- stays there while the OSD comes and goes.
local theme = require("theme")
local presentation = require("presentation")

local album = {}

-- The region the cover fits in: between the left margin and the right one,
-- and from under the header block to above the scrubber.
local REGION_X = theme.margin.x
local REGION_TOP = 300
local REGION_BOTTOM = 832
local BOX_H = REGION_BOTTOM - REGION_TOP
local CENTER_Y = REGION_TOP + BOX_H / 2

local function box_w()
  return theme.canvas.w - 2 * REGION_X
end

local function center_x()
  return REGION_X + box_w() / 2
end

-- The overlay id the cover owns. The logo owns id 1 and the trickplay tile owns
-- id 2, so the cover keeps a third id in overlay-add's own numbering.
local OVERLAY_ID = 3

-- The script-message name the display and the bridge agree on.
local ART_REQUEST = "liken-art-request"

local redraw_cb = function() end
function album.set_redraw(fn)
  redraw_cb = fn
end

-- The decoded cover the bridge returned, nil until the bridge answers and nil
-- again for an item the bridge has no cover for.
-- A new item drops the cover it holds. The tracks of an album are the
-- chapters of one item and never reach here, so nothing within an album
-- blinks.
local blob = nil
-- The pixel box of the request in flight, so the same box is not asked for
-- twice.
local want = nil
-- The box the bridge has already answered for. A redraw then asks once, and
-- an answer of no cover ends the asking instead of starting it again.
local answered = nil
-- The overlay on screen now, so a redraw places it again only when the bitmap or
-- its position changes.
local placed = nil

-- Read how the canvas maps to the real screen, the way the trickplay tile does.
-- sx maps a canvas x to real pixels, sy maps a canvas y, and overlay-add places
-- in real pixels, so the cover must use both to sit where the ass text draws.
local function osd_metrics()
  return theme.osd_scale()
end

-- Remove the cover overlay when it is on screen.
local function clear()
  if placed then
    mp.command_native({ "overlay-remove", OVERLAY_ID })
    placed = nil
  end
end

-- request asks the bridge for the playing track's cover at the pixel box the
-- region gives it. The bridge fits the cover inside the box and keeps the
-- aspect, so the returned bitmap may be smaller. It sends nothing for an item
-- that is not music, before the screen size is known, or when the same box is
-- already in flight or already answered.
local function request()
  if presentation.type() ~= "music" then
    return
  end
  local m = osd_metrics()
  if not m then
    return
  end
  local w = math.floor(box_w() * m.sx + 0.5)
  local h = math.floor(BOX_H * m.sy + 0.5)
  if w <= 0 or h <= 0 then
    return
  end
  local key = w .. "x" .. h
  if key == want or key == answered then
    return
  end
  want = key
  mp.command_native({ "script-message", ART_REQUEST, "album", tostring(w), tostring(h) })
end

-- album.on_item runs when the playlist reaches a new item. It drops the previous
-- item's cover, removes its overlay, and asks for the new item's cover.
-- An item with no cover leaves the overlay removed, so a film after a music
-- item carries no cover parked over it, and a music item the bridge has no
-- cover for shows none of the last item's.
function album.on_item()
  blob = nil
  want = nil
  answered = nil
  clear()
  request()
  redraw_cb()
end

-- album.on_resize runs when the screen size changes. It asks for the cover at
-- the new box, and keeps the current bitmap on screen until the new one arrives,
-- so the cover does not blink on a resize.
-- It forgets no key. A new box differs from the one in flight and from the
-- one answered, so request asks on its own, and an osd-dimensions change
-- that leaves the box where it was asks nothing.
function album.on_resize()
  request()
end

-- album.on_art receives one decoded cover from the bridge. Accept only an album
-- reply, so a tile reply routed here by mistake draws nothing.
-- The bridge answers every request, and an empty path is its answer for an
-- item it found no cover for. Either answer marks the box answered, so the
-- display asks once for a box, and a missing cover ends the asking.
function album.on_art(kind, path, w, h, stride)
  if kind ~= "album" then
    return
  end
  answered = want
  if not path or path == "" then
    blob = nil
    clear()
    redraw_cb()
    return
  end
  blob = { path = path, w = tonumber(w), h = tonumber(h), stride = tonumber(stride) }
  redraw_cb()
end

-- sync places the cover for a music item and removes it for anything else.
-- The cover does not track the OSD, because a music screen with the OSD down
-- would otherwise be a black frame.
function album.sync()
  if presentation.type() ~= "music" then
    clear()
    return
  end
  local m = osd_metrics()
  if not m then
    return
  end
  request()
  if not blob then
    return
  end
  local x = math.floor(center_x() * m.sx + 0.5) - math.floor(blob.w / 2)
  local y = math.floor(CENTER_Y * m.sy + 0.5) - math.floor(blob.h / 2)
  if x < 0 then
    x = 0
  end
  if y < 0 then
    y = 0
  end
  local sig = table.concat({ blob.path, x, y, blob.w, blob.h, blob.stride }, ":")
  if sig ~= placed then
    mp.command_native({ "overlay-add", OVERLAY_ID, x, y, blob.path, 0, "bgra", blob.w, blob.h, blob.stride })
    placed = sig
  end
end

return album
