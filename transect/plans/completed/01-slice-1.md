# 01: Slice 1, structure and health

Slice 1 draws the cluster elevation from the Kubernetes API and
Prometheus, run from a workstation.  It is built in stages, and each
stage ends in something to reload and look at against a real cluster.
A session can take one stage, or stop partway through one and iterate
on what it shows.

## Decisions

- **One binary crate.**  It becomes a workspace when the flow agent
  arrives in slice 2.
- **Modules by domain:**

  | Module | Holds |
  |---|---|
  | `main.rs` | flags (`--kubeconfig`, `--prometheus`, `--resources`, `--listen`) and wiring |
  | `cluster/` | kube-rs reflectors for Namespace, Node, Pod, Deployment, StatefulSet, DaemonSet, CronJob, and one "changed" signal |
  | `layers/` | the `Layers` resource, loading from `--resources`, selector matching, the annotation override, the unsorted band |
  | `machines/` | machine strips: allocatable, requested, used |
  | `prometheus.rs` | a small instant-query client |
  | `map/` | the cluster elevation's view model, with its templates and CSS beside it |
  | `web/` | the axum router, the event stream, static files, the brand |

- **Templates: askama.**  Typed views turn a renamed field or a
  misspelled variable into a compile error, which matters when the
  page is filled from a large graph.  A template-only rebuild takes
  under a second in a crate with axum and kube in it.  The cost is a
  restart, which re-lists the cluster; debug builds read CSS from disk
  so a CSS change needs no restart.  `askama.toml` sets
  `dirs = ["src"]` so templates sit next to their module, and
  `blocks = [...]` renders one block of a page for a pushed fragment.
  minijinja reloads templates in process, and is the fallback if
  restarts become the slow part of the loop.
- **Live updates.**  Any watch event marks the map dirty.  After a
  debounce of about 500 ms, the server renders the changed block and
  pushes it over server-sent events, and htmx's `sse-swap` replaces
  it.  Whole blocks, not single parcels, until a page needs finer.
- **htmx and its SSE extension are vendored** and embedded, like
  every other static file.  The page asks for nothing over the
  network.
- **The brand is `liken`'s.**  transect may ship as a `liken` component,
  so it takes its colors from `liken`'s `brand/liken.css` and sets text
  in Source Sans 3 from `brand/fonts/`, copied into `src/web/brand/`
  with the `liken` commit they came from.  Names and numbers set in the
  system monospace.  Light and dark follow the system setting, and
  the design is drawn dark first.
- **Colors map from the comps onto `liken`'s tokens:**

  | Comps | Role | `liken` |
  |---|---|---|
  | near-black grays | page, band, parcel | `--page`, `--row`, `--panel` |
  | border grays | borders | `--rule`, plus one lighter step |
  | light grays | text | `--ink`, `--ink-muted`, plus one dimmer step |
  | blue | links, selection | `--link` (pale sage), deep moss `#4a5d3a` for selected fills |
  | orange | failure | `--warm`, filled |
  | amber | warning | `--warm`, as an outline |
  | green | the "all clear" dot | mid sage `#6e8352` |

- **Parcels size by workload count:** `flex: W 1 (30·W)px`, heaviest
  first, with a minimum for a namespace with none.

## Stages

### 1. Bands

The header and one band per layer from `--resources`, each holding a
parcel per namespace with its workload count, and an unsorted band
for namespaces that match no layer.  Read once at start; no event
stream yet.  Unit tests cover selector matching (`matchLabels`, each
`matchExpressions` operator, several selectors on one layer), the
annotation override, and the unsorted band.

### 2. Live

The event stream.  Creating or deleting a namespace, or scaling a
workload into a new count, shows on the open page within a second or
two, with no reload.

### 3. Machines

The machines band: one strip per Node with its name, CPU and memory
bars of requested against allocatable, and its pod count, all from
the API.  A machine's role (a control-plane node, a GPU) comes from
its labels.

### 4. Used

The used bar on each strip, from Prometheus: CPU and memory used per
node, refreshed on the scrape interval.  Without `--prometheus`, the
strips draw without it and say so.

### 5. Health

Parcels stay gray while their pods are ready, and take `--warm` when
they are not: an outline for restarts or pods not ready, a fill for a
workload with no ready replicas.  The rule names what it saw, so the
sidebar can repeat it.

### 6. The sidebar

"What to know now" when nothing is selected: the findings built into
slice 1 as templates, such as a machine whose requests nearly fill it
or that uses more memory than it requested.  Alerts from
Alertmanager come later; until then the sidebar says it has no alert
source.

## Not in slice 1

- The ground band: it needs the storage edges.
- Splitting a namespace into several parcels, such as one per
  operator in a namespace that runs several.
- Selection, cards, and the elevations below the cluster.
- Search and "size parcels by."
