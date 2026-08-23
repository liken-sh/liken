-- The audio-track control. Its cell shows the current track, and its
-- chooser lists the audio tracks that track-list carries. select switches
-- aid to the picked track.
local theme = require("theme")
local chooser = require("chooser")

local audio = {}

local redraw_cb = function() end
function audio.set_redraw(fn)
  redraw_cb = fn
end

local open = false
local sel = 1

local function tracks()
  local out = {}
  for _, t in ipairs(mp.get_property_native("track-list") or {}) do
    if t.type == "audio" then
      out[#out + 1] = t
    end
  end
  return out
end

function audio.available()
  return #tracks() >= 1
end

-- Name a track by what a viewer recognizes: the title and the language.
-- When the file gives neither, the id is the only handle left, so the label
-- falls back to it.
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

-- The focused cell draws full brightness with a green caption. The other
-- cells draw subdued, so brightness alone marks which control has focus.
function audio.draw(x, y, focused)
  local ts = tracks()
  local cur = selected_index(ts)
  local status = cur and label_for(ts[cur]) or "off"
  local alpha = focused and theme.alpha.opaque or theme.alpha.subdued
  local caption = focused and theme.color.fill or theme.color.muted
  local parts = {}
  parts[#parts + 1] = theme.text(x, y, "AUDIO", theme.type.small, caption, 7, alpha)
  parts[#parts + 1] = theme.text(x, y + 40, status, theme.type.label, theme.color.text, 7, alpha)
  return table.concat(parts, "\n")
end

function audio.activate()
  local ts = tracks()
  sel = selected_index(ts) or 1
  open = true
  return audio
end

function audio.is_open()
  return open
end

function audio.close()
  open = false
end

-- The chooser is a vertical list, so up moves to the previous entry and down
-- to the next. select applies the picked track and closes.
function audio.handle(action)
  local ts = tracks()
  if action == "up" then
    sel = math.max(1, sel - 1)
  elseif action == "down" then
    sel = math.min(#ts, sel + 1)
  elseif action == "select" then
    local t = ts[sel]
    if t then
      mp.set_property("aid", tostring(t.id))
    end
    open = false
  end
end

function audio.draw_chooser()
  local entries = {}
  for _, t in ipairs(tracks()) do
    entries[#entries + 1] = label_for(t)
  end
  return chooser.draw("Audio", entries, sel)
end

return audio
