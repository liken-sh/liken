-- The header, the passive top-left region. It draws the current item's name
-- from the resolved presentation fields, and takes no focus. A movie shows
-- its title. A series shows the series name over a season and episode line.
-- When the item carries a logo, the header shows the logo in place of that
-- name. It asks the bridge to decode the logo to a pixel size, and places the
-- returned bitmap with overlay-add.
local theme = require("theme")
local presentation = require("presentation")

local header = {}

local LEFT = theme.margin.x
local TOP_Y = 90
-- The second line sits below the title, far enough to clear the title glyphs.
local SECOND_Y = TOP_Y + 82
-- With a logo, the second line sits this far below the logo's own bottom.
local SECOND_GAP = 26

-- The logo's largest box, in canvas coordinates. It sits where the title line
-- does, and is about that line's height, so the second line below it stays
-- clear. The bridge scales the logo to fit inside this box.
local LOGO_MAX_W = 760
local LOGO_MAX_H = 110

-- The overlay id the logo owns. overlay-add numbers its bitmaps in a space of
-- its own, so the logo keeps one id and a later bitmap takes another.
local OVERLAY_ID = 1

-- The two script-message names the display and the bridge agree on.
local ART_REQUEST = "liken-art-request"
local ART_REPLY = "liken-art"

local redraw_cb = function() end
function header.set_redraw(fn)
  redraw_cb = fn
end

-- The logo the current item declared, nil when it has none.
local logo_uri = nil
-- The decoded bitmap the bridge returned. It is nil until the bridge answers,
-- and a swap clears it. When it is present, the header draws no title text.
local blob = nil
-- The pixel size last asked for, so an unchanged size is not asked for again.
local want = nil
-- The signature of the overlay on screen now, so a redraw places it again only
-- when the bitmap or its position changes.
local placed = nil

-- A season or an episode arrives as a JSON number. Render it as a whole
-- number, so the line reads "Season 2", not "Season 2.0".
local function num(v)
  if type(v) == "number" then
    return string.format("%d", v)
  end
  return tostring(v)
end

-- The month names, indexed one to twelve, so a date reads with its month
-- spelled out.
local MONTHS = {
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
}

-- format_date turns an ISO date, YYYY-MM-DD, into a readable form like
-- "March 5, 2017". A string that is not an ISO date returns unchanged, so a
-- library that hands a ready-made date shows it as it is.
local function format_date(date)
  local y, m, d = date:match("^(%d%d%d%d)-(%d%d)-(%d%d)$")
  if y then
    local month = MONTHS[tonumber(m)]
    if month then
      return month .. " " .. tonumber(d) .. ", " .. y
    end
  end
  return date
end

-- The second line joins the season, the episode number, and the episode
-- title, and shows only the parts the item declared.
local function second_line()
  local segs = {}
  local season = presentation.season()
  local episode = presentation.episode()
  local etitle = presentation.episode_title()
  local date = presentation.date()
  if season ~= nil then
    segs[#segs + 1] = "Season " .. num(season)
  end
  if episode ~= nil then
    segs[#segs + 1] = "Episode " .. num(episode)
  end
  if etitle and etitle ~= "" then
    segs[#segs + 1] = etitle
  end
  if date and date ~= "" then
    segs[#segs + 1] = format_date(date)
  end
  if #segs == 0 then
    return nil
  end
  return table.concat(segs, "  \194\183  ")
end

-- osd_metrics reads how the canvas maps to the real screen. sx maps a canvas x
-- to real pixels, and sy maps a canvas y, and overlay-add places in real
-- pixels, so the logo must use both to sit where the ass text draws. The
-- osd-dimensions margins describe where the video letterboxes, not where the
-- overlay draws, so they take no part here.
local function osd_metrics()
  return theme.osd_scale()
end

-- request asks the bridge for the current logo at the pixel size the screen
-- needs. It sends nothing when the item has no logo, when the screen size is
-- not known yet, or when the header already holds the logo at that size.
local function request()
  if not logo_uri then
    return
  end
  local m = osd_metrics()
  if not m then
    return
  end
  local w = math.floor(LOGO_MAX_W * m.sx + 0.5)
  local h = math.floor(LOGO_MAX_H * m.sy + 0.5)
  if w <= 0 or h <= 0 then
    return
  end
  local key = w .. "x" .. h
  if key == want and blob then
    return
  end
  want = key
  mp.command_native({ "script-message", ART_REQUEST, "logo", tostring(w), tostring(h) })
end

-- header.on_item runs when the playlist reaches a new item. It drops the
-- previous item's logo, removes its overlay, and asks for the new item's logo.
-- An item with no logo leaves the overlay removed, and the header falls back to
-- text.
function header.on_item()
  logo_uri = presentation.logo()
  blob = nil
  want = nil
  if placed then
    mp.command_native({ "overlay-remove", OVERLAY_ID })
    placed = nil
  end
  request()
  redraw_cb()
end

-- header.on_resize runs when the screen size changes. It asks for the logo at
-- the new size, and keeps the current bitmap on screen until the new one
-- arrives, so the logo does not blink on a resize.
function header.on_resize()
  want = nil
  request()
end

-- header.on_art receives one decoded bitmap from the bridge. It holds the blob
-- for sync to place. It drops a reply for an item that no longer has a logo.
function header.on_art(kind, path, w, h, stride)
  if kind ~= "logo" or not logo_uri then
    return
  end
  blob = { path = path, w = tonumber(w), h = tonumber(h), stride = tonumber(stride) }
  redraw_cb()
end

-- header.sync places or removes the logo overlay to match the display state.
-- The logo shows only while the OSD is up and a bitmap is ready. It is placed
-- in real pixels, because overlay-add does no scaling of its own.
function header.sync(visible)
  if visible and blob then
    local m = osd_metrics()
    if not m then
      return
    end
    local x = math.floor(LEFT * m.sx + 0.5)
    local y = math.floor(TOP_Y * m.sy + 0.5)
    local sig = table.concat({ blob.path, x, y, blob.w, blob.h, blob.stride }, ":")
    if sig ~= placed then
      mp.command_native({ "overlay-add", OVERLAY_ID, x, y, blob.path, 0, "bgra", blob.w, blob.h, blob.stride })
      placed = sig
    end
  elseif placed then
    mp.command_native({ "overlay-remove", OVERLAY_ID })
    placed = nil
  end
end

-- With a logo, the second line clears the logo's actual height, because a logo
-- scales to its own size within the box. Without a logo, or before the screen
-- size is known, it falls to the fixed line under the title.
local function second_y()
  if blob then
    local m = osd_metrics()
    if m then
      return TOP_Y + math.floor(blob.h / m.sy + 0.5) + SECOND_GAP
    end
  end
  return SECOND_Y
end

-- A series shows the series name over the season line. Everything else with a
-- title shows the title. A field that resolved to nothing draws nothing. A
-- logo takes the place of that name, and the second line stays as text.
function header.draw()
  local parts = {}
  local has_logo = blob ~= nil
  local sy = second_y()
  if presentation.hint() == "series" then
    if not has_logo then
      local series = presentation.series() or presentation.title()
      if series then
        parts[#parts + 1] = theme.text(LEFT, TOP_Y, series, theme.type.title, theme.color.text, 7)
      end
    end
    local line = second_line()
    if line then
      parts[#parts + 1] = theme.text(LEFT, sy, line, theme.type.small, theme.color.muted, 7)
    end
  else
    local title = presentation.title()
    if not has_logo and title then
      parts[#parts + 1] = theme.text(LEFT, TOP_Y, title, theme.type.title, theme.color.text, 7)
    end
    if has_logo or title then
      local year = presentation.year()
      if year ~= nil then
        parts[#parts + 1] = theme.text(LEFT, sy, num(year), theme.type.small, theme.color.muted, 7)
      end
    end
  end
  if #parts == 0 then
    return nil
  end
  return table.concat(parts, "\n")
end

return header
