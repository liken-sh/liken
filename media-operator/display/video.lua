-- The video-track control. It mirrors the audio control, and it switches `vid`
-- among the file's video tracks.
local theme = require("theme")
local chooser = require("chooser")

local video = {}

local redraw_cb = function() end
function video.set_redraw(fn)
  redraw_cb = fn
end

local open = false
local sel = 1

local function tracks()
  local out = {}
  for _, t in ipairs(mp.get_property_native("track-list") or {}) do
    if t.type == "video" then
      out[#out + 1] = t
    end
  end
  return out
end

-- The video control shows only when the file carries more than one video track,
-- like an alternate angle. Most files carry one, so it stays hidden.
function video.available()
  return #tracks() > 1
end

-- Name a track by its title and its language. When the file gives neither, the
-- id is the only handle left, so the label falls back to it.
local function label_for(t)
  local lang = t.lang and t.lang:upper() or nil
  if t.title and t.title ~= "" then
    if lang then
      return t.title .. " (" .. lang .. ")"
    end
    return t.title
  end
  if lang then
    return lang
  end
  return "Track " .. tostring(t.id)
end

local function selected_index(ts)
  for i, t in ipairs(ts) do
    if t.selected then
      return i
    end
  end
  return nil
end

function video.value()
  local ts = tracks()
  local cur = selected_index(ts)
  return cur and label_for(ts[cur]) or "off"
end

function video.draw(x, y, focused)
  local color = focused and theme.color.fill or theme.color.text
  local alpha = focused and theme.alpha.opaque or theme.alpha.subdued
  return theme.text(x, y, video.value(), theme.type.tiny, color, 4, alpha)
end

function video.activate()
  local ts = tracks()
  sel = selected_index(ts) or 1
  open = true
  return video
end

function video.is_open()
  return open
end

function video.close()
  open = false
end

-- The chooser is a vertical list, so up moves to the previous entry and down
-- to the next. select applies the picked track and closes.
function video.handle(action)
  local ts = tracks()
  if action == "up" then
    sel = math.max(1, sel - 1)
  elseif action == "down" then
    sel = math.min(#ts, sel + 1)
  elseif action == "select" then
    local t = ts[sel]
    if t then
      mp.set_property("vid", tostring(t.id))
    end
    open = false
  end
end

function video.draw_chooser()
  local entries = {}
  for _, t in ipairs(tracks()) do
    entries[#entries + 1] = label_for(t)
  end
  return chooser.draw(entries, sel)
end

return video
