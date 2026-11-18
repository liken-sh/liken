## The OpenAPI document

`media-api` serves the OpenAPI document that this page is generated
from at `/v1/media/openapi.json`. The repository keeps a copy of it in
`media-operator/openapi.json`, and a test fails when that copy differs
from the router's routes. The served copy differs from the committed one
only in its `servers` entry, which is the origin that the request
reached.
