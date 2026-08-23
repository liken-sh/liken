-- The presentation module holds the current item's declared fields and
-- resolves each one for the header. The command sidecar hands it a block over
-- a script-message whenever the playlist reaches a new item.
local utils = require("mp.utils")

local presentation = {}

local redraw_cb = function() end
function presentation.set_redraw(fn)
  redraw_cb = fn
end

-- The current item's declared fields. An empty table means the item declared
-- nothing, so every field falls through to its next tier.
local block = {}

-- receive takes the block as one JSON string. An empty object, or text that
-- does not parse, means the item declared nothing, so every field falls
-- through to its next tier.
function presentation.receive(text)
  local parsed = nil
  if text and text ~= "" then
    parsed = utils.parse_json(text)
  end
  if type(parsed) == "table" then
    block = parsed
  else
    block = {}
  end
  redraw_cb()
end

-- The title resolves in three tiers: the block's own title, then mpv's
-- media-title read from the file, then nothing. The other fields have no file
-- to fall back to, so they come from the block alone.
function presentation.title()
  if block.title and block.title ~= "" then
    return block.title
  end
  local media = mp.get_property("media-title")
  if media and media ~= "" then
    return media
  end
  return nil
end

function presentation.type()
  return block.type
end

function presentation.hint()
  return block.hint
end

function presentation.series()
  return block.series
end

function presentation.season()
  return block.season
end

function presentation.episode()
  return block.episode
end

function presentation.episode_title()
  return block.episodeTitle
end

function presentation.year()
  return block.year
end

function presentation.date()
  return block.date
end

return presentation
