# 10: Vulnerabilities

The owner asked to "show currently active vulnerability reports with
their CVE numbers and links out to them for each workload", and then
for "a special page covering CVEs".

A vulnerability scanner that runs in the cluster, such as
trivy-operator, writes one report object per workload container.
transect already watches every custom kind and reads what an operator
ships about it as kind knowledge.  So kind knowledge for the scanner's
report kind says how a report names its workload and container, which
image digest it scanned, when, and where its list of findings is, with
the fields that hold each finding's id, severity, score, package,
installed and fixed versions, and link.  The code knows a findings
report as a shape that knowledge describes, and `src/` names no
scanner and no report kind.

## Built

Every stage is built:

0. This plan (133ed49).
1. **The knowledge, the store, and the index** (c5c32b3).  A trimmed
   list now keeps an item that lacks one of the named fields, as an
   empty object, so two paths into one list still merge item by item;
   before, a finding with no score emptied the whole list.
2. **`/vulnerabilities` and an id's page** (cfe68c9), the masthead link,
   and search.
3. **A workload's tab and its count line** (319fdb2).  The count line
   is on the workload's card, so it shows on every page the card does,
   beside the access tab's "2 ways in".
4. **Check against the real cluster**, in the commit that adds this
   section.  The pages' ids, workloads, and counts matched what
   `kubectl get vulnerabilityreports` and the pods' `imageID`s give,
   and every report's summary agreed with its own findings.  The
   search page's masthead read no values and left the link out, which
   this commit fixes.

## Open questions

- **A CronJob's reports are all stale between runs.**  By the rule
  above, a CronJob runs no digest while no Job pod runs, and in a
  home cluster that leaves out a fifth of all reports, nearly all of
  them for a digest a finished Job pod still in the cluster ran.
  Counting the digests of a CronJob's finished pods as current would
  bring them back; it bends "runs now".
- A report whose owner is a bare Pod, or an old ReplicaSet already
  gone, names no workload with a page, and its name shows as text.

## Decisions

- **It states what the scanner reported, and when.**  transect makes
  no judgment of its own: no ranking of its own, and no finding beside
  the scanner's.  A report's box reads "trivy-operator
  reports 12 findings in the image of app, scanned 3 hours ago", with
  the scanner named by the last part of the image its kind knowledge's
  `controller.image` names, or by the report kind when the knowledge
  names no controller.
- **Severity and score are the scanner's own.**  The severity is shown
  as the scanner writes it, and the score as the number it writes, such
  as a CVSS base score.  The score is gray.  The severity word is red
  for `CRITICAL` and the brand's warm orange for `HIGH`, and gray for
  every other, wherever it is named: the severity column of every
  findings table, the id's page, and the count lines on a workload's
  card and its tab, where only a count above zero shows.  The red is
  `--critical` in `vulnerabilities.css`, a muted `#a8312c` in the light
  scheme and `#ef7a70` in the dark, at contrast ratios of 6.7 and 6.2
  on `--page` and `--panel` (light) and 6.5 and 5.6 (dark).  `liken`'s
  brand has no red, so transect defines this one.  Nothing on the
  cluster map, and nothing in the Now list.
- **Every severity shows by default.**  A home cluster's scanner is
  configured to report only critical and high findings, yet three
  quarters of what it writes says `UNKNOWN`, nearly all of it with a
  score of 7 or more, so hiding any severity by default would hide most
  of what the scanner chose to report.  A query parameter,
  `?severity=CRITICAL,HIGH`, narrows a page to the severities it names,
  and each page links each severity the scanner used, so no script is
  needed.
- **One row per finding id.**  A scanner writes one finding per package
  an id affects, so an id repeats.  Every table lists each id once, and
  its packages are a list inside the row, one item per package with its
  installed and fixed versions in sub-columns that line up down the
  page (`/vulnerabilities` lists each package with the versions that
  fix it, and the workloads that run it).  Identical items collapse into
  one, since several binaries in one image may hold the same module at
  the same version.  `/vulnerabilities/{id}` has one row per workload and
  container, with that container's packages and the scan time.  A fix
  is a version in that list, or a gray dash.
- **A row shows its most severe severity and its highest score.**  When
  the packages of one id carry different severities or scores, the row
  shows the most severe severity, in the order below, and the highest
  score, and the sort uses those.
- **Tables sort ids by severity first**, in the order of the knowledge's
  `summary` list, then any other severity, with `UNKNOWN` last; within a
  severity, an id with a fixed version for some package first, then by
  score, highest first, with no score last, then by id.  The severity,
  score, and id cells never wrap.
- **A finding's title is the scanner's, from the report.**  The optional
  `item.title` is kept in the trimmed store with the other item fields,
  and shared in the index like them.  It shows in gray under the id in a
  table, wrapping, and as the lead line under the id on its page.  transect fetches nothing for it.
- **A count is of finding ids.**  An id counts once, at its most
  severe severity, however many packages it affects.  The count line on
  a workload's card, such as "3 critical, 12 high, 40 unknown", counts
  the distinct ids of all the workload's current reports, so an id in two
  containers counts once; a container's box on the tab says "reports N
  finding ids" and counts that report's ids; and `/vulnerabilities` says
  "N finding ids" across every current report.  Lines are colored by
  severity as above.  The report kind's page shows the scanner's own
  summary counts, which are of findings, one per package.
- **A report is current when the image digest it names is one the
  workload runs now**, as its pods' container statuses report it in
  `imageID`, for the container the report names.  A report of another
  digest is left out of every count and every list but its workload's
  tab, where it shows as stale, with the digest it names.  A workload
  with no running pod, such as a CronJob between runs, runs no digest,
  so its reports are all stale.
- **An id opens its own page, and that page links out.**  An id, such
  as a CVE number, opens `/vulnerabilities/{id}` wherever it shows, and
  that page and a workload's tab link to the scanner's own link for it
  (`primaryLink` for trivy-operator), which is only followed when it is
  a plain `http` or `https` URL.
- **Names follow the project rule**: `Kind namespace/name`, without the
  namespace on a page that is the namespace's own.  Every page is a
  board of boxes, and a wide table takes the page's full width with no
  scroll box of its own.
- **The scanner's other reports are not read.**  A scanner also writes
  reports of secrets it found in images.  No knowledge here describes
  them, and no page renders their contents.

### The `findings` knowledge

KindKnowledge gains `findings`:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: vulnerabilityreports.aquasecurity.github.io
spec:
  controller: {image: aquasec/trivy-operator}
  findings:
    subject:
      owner: true
      container: {label: trivy-operator.container.name}
    digest: report.artifact.digest
    scannedAt: report.updateTimestamp
    summary:
      - {severity: CRITICAL, path: report.summary.criticalCount}
      - {severity: HIGH, path: report.summary.highCount}
      - {severity: MEDIUM, path: report.summary.mediumCount}
      - {severity: LOW, path: report.summary.lowCount}
      - {severity: UNKNOWN, path: report.summary.unknownCount}
    items: "report.vulnerabilities[]"
    item:
      id: vulnerabilityID
      severity: severity
      score: score
      package: resource
      installed: installedVersion
      fixed: fixedVersion
      link: primaryLink
      title: title
```

| Field | Means |
|---|---|
| `subject.owner` | the report's controller owner reference names what it scanned, followed to its top owner as a pod's is; `true` is the one form read today |
| `subject.container` | the `label` or `annotation` that names the scanned container |
| `digest` | the field holding the digest of the image scanned, `sha256:…` |
| `scannedAt` | the field holding when the scan ran |
| `summary[]` | the scanner's severities, most severe first, each with the field holding the report's own count of it; the order sorts the count line |
| `items` | the list of findings, with `[]` |
| `item.id`, `item.severity` | the fields of one finding holding its id and its severity; both are required |
| `item.score`, `item.package`, `item.installed`, `item.fixed`, `item.link`, `item.title` | the optional fields holding its score, its package, the version installed, the first version with a fix, the scanner's link for it, and its one-line title |

A field of `item` is a path inside one finding.  An empty string reads
as not set, so a finding whose `fixedVersion` is `""` has no fix.

### The findings are kept in the store

A kind with `findings` knowledge stays watched trimmed, and its trimmed
objects also keep the fields the knowledge names, copied to the same
paths, as a GitOps tool's kinds keep theirs: the metadata, the digest,
the scan time, the summary counts, and for each finding only the item
fields.  A finding's description, and every other field of the report,
never reach the store.  The watch already receives each
report whole and trims it in memory, so this costs no request and no
poll, and both pages render from the model as every other page does.

The model reads the trimmed reports once per build into an index: each
report with its workload, container, digest, scan time, and findings,
whether it is current, and each id to the reports that hold it.  The
index's strings are shared, so an id, a package, or a version held by
many reports is held once.

Measured on a home cluster before this was built: 439 reports, 14.8
MB of JSON as `kubectl` lists them, 15,429 findings, and 584 distinct
ids.  The release build's resident memory against that cluster, two
minutes after the first page answered, was 176 MB before stage 1
(133ed49) and 166 MB after it (c5c32b3), with every report's findings
in the store.  The 10 MB less is the spread between two runs, not a
saving: the stores hold about 15,000 small finding objects of six or
seven short strings each, a few megabytes, well under the 25 MB this
plan allowed.

### Pages and URLs

| URL | Shows |
|---|---|
| `/vulnerabilities` | the severity links, one sentence, and a box of every id a current report holds, with its title, severity, score, its packages and their fixed versions, and the workloads that run it; a "↗" beside an id opens the scanner's link in a new tab when it is `http` or `https` |
| `/vulnerabilities/{id}` | what the scanner says of one id: its severity, score, and link, and a table of every workload that runs it, by container, with its package, installed and fixed versions, and when it was scanned |
| `…/{resource}/{name}/vulnerabilities` | a workload's tab: a box per container's current report, with its findings, and a box of its stale reports, each with its digest |

Each takes `?severity=` with a comma-separated list.  The masthead
links "vulnerabilities" beside "access" and "kinds" only when some
loaded kind knowledge has `findings`.  The tab shows on a workload that
some report names.  Search finds an id a current report holds and opens
its page.  The report kind's own page, `/kinds/{resource}`, shows each
report's workload, container, digest, and whether it is current, which
is where a person writing the knowledge sees it take effect, beside
`/resources`, where a `findings` block that does not validate is named.

## Stages

0. This plan, in full.
1. The `findings` knowledge, the store's kept fields, the current-digest
   rule, and the model's index, shown on the report kind's page.  The
   fixture gains reports: a current one, a stale one, a workload with
   two containers, a finding with and without a fix, one with severity
   `UNKNOWN` and a score, one with no score, and one id two workloads
   share.  `examples/trivy-operator/` gets the knowledge.  The release
   build's resident memory is measured against a home cluster
   before and after, and recorded here; past 25 MB more, the build
   stops.
2. `/vulnerabilities` and `/vulnerabilities/{id}`, the masthead link,
   and search.
3. A workload's tab and its count line.
4. Check it against the real cluster, compare ids and counts with
   `kubectl get vulnerabilityreports`, and fix what differs.  The new
   pages get rows in `src/web/served.rs` and text goldens.

The scanner's other report kinds, such as config audits, are not part
of this plan.
