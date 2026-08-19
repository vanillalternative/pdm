# pdm — Portuguese municipal PDM/IGT spatial query

`pdm` is a command-line tool that answers one question:

> **Given a coordinate or a parcel polygon, what planning/zoning rules and constraints apply at that exact location?**

It is *not* a downloader or a summary of the whole municipal plan. It resolves the
location to a municipality, finds the planning instrument (PDM/IGT) in force, and
intersects the point/polygon against the official planning layers — returning the
concrete zoning class, the constraints (RAN, REN, …) that hit that spot, **and the
regulation articles that apply to that zoning category**, with source attribution,
a confidence level, and links to the official documents.

## Design philosophy — an AI enabler with no AI inside

`pdm` **assembles**, it does not **interpret**. Given coordinates it deterministically
gathers all the relevant public data — zoning class, constraints, and the verbatim
text of the applicable *Regulamento* articles — and hands it over as a structured
evidence pack (ideal as JSON for a downstream AI or a human to reason over). It
deliberately does **not** compute "you can build 2 floors at índice 0.8": extracting
the numeric building envelope is interpretation, and that is the job of the AI or
person consuming the output, not of the tool. The tool contains no model and makes
no legal judgement. The legally binding answer only ever comes from a **PIP** to the
Câmara (RJUE) — automated data gets you most of the way, never the certificate.

> ⚠️ **Legal disclaimer.** This is an automated query against public planning data
> (an approximation). It is **not** an official municipal certificate, a *certidão*,
> or a legal opinion. Always confirm with the competent municipality (*Câmara
> Municipal*) before making any decision.

## Status

**All mainland municipalities are supported for zoning and the national
constraints.** Any coordinate/parcel in continental Portugal resolves to its
municipality and freguesia (bundled CAOP boundaries), gets its zoning from the
national **DGT CRUS** dataset — the harmonised carta of every municipality's
plans in force — and is checked against the national **DGT SRUP** servidões:
**Rede Natura 2000** (ZPE and ZEC) and the **rural fire hazard** chart
(perigosidade de incêndio rural, high classes only, which are the ones that
restrict building). All fetched live (bbox-filtered) and cached.

Two support levels:

- **Full** (dedicated adapter): the above **+ municipal constraint layers +
  Regulamento articles**. Pilot: **Tomar** (concelho 1418).
- **National-data-only** (generic adapter): every other mainland municipality.
  The municipal constraint layers (RAN, REN, servidões locais — e.g. Tomar's
  Albufeira de Castelo de Bode/POACB) and the written regulation are
  municipality-specific work and are added one municipality at a time; until
  then results carry an explicit note and are capped at **low confidence** —
  the absence of a constraint in the output does not mean it doesn't exist.

The Azores and Madeira are not yet covered: the mainland DGT datasets
(CAOP/CRUS) exclude them, and the regional services are not yet integrated
(queries there say so explicitly).

## Install / build

```sh
go build -o pdm ./cmd/pdm
```

Requires Go 1.24+. There is no CGo dependency; all geometry and the coordinate
projection are pure Go.

## Usage

```
pdm <lat> <lon>                 query a coordinate (shorthand)
pdm point <lat> <lon>           query a coordinate
pdm polygon <file.geojson>      query a parcel polygon
pdm report <file.geojson|lat lon> [--format ...]   full report
pdm analyse <file.geojson|lat lon> [--tier ...]    AI-written analysis report
pdm supported                   show municipality coverage/support levels
pdm version
pdm help
```

Options:

| flag | meaning |
|---|---|
| `--format text\|json\|markdown\|html` | output format (default `text`; `analyse` defaults to `html`) |
| `--tier basic\|premium` | `analyse` model tier (default `basic`) |
| `--live` | fetch fresh data from the official geoservices (falls back to bundled) |
| `--no-cache` | do not read/write the local cache |
| `--cache-dir <dir>` | override the cache directory |

**Coordinates are `latitude longitude`, WGS84 decimal degrees.** Portuguese
longitudes are negative (west of Greenwich) — `pdm 39.60 -8.41` works; the parser
treats `-8.41` as a coordinate, not a flag.

### Examples

```sh
pdm 39.60 -8.41
pdm point 39.60 -8.41
pdm polygon ./testdata/parcel.geojson
pdm report ./testdata/parcel-large.geojson --format json
pdm report 39.60 -8.41 --format markdown
pdm point 39.60 -8.41 --format html > report.html   # standalone page + map
```

### HTML report

`--format html` writes a complete, self-contained HTML page to stdout: a
locator **map** (inline SVG — municipality boundary, the zoning polygon, nearby
constraint zones colour-coded, and the point/parcel), followed by the zoning,
constraint chips (yes/no), the applicable regulation articles (collapsible, with
full text), sources, and the disclaimer. It's theme-aware and needs no network
or assets — open it in any browser or share the file.

Point output:

```
Input:        39.60000, -8.41000 (lat, lon)
Municipality: Tomar
Applicable plan: PDM de Tomar

Zoning:
  - Solo Urbano – Espaços Centrais Nível 1

Constraints:
  - RAN: no
  - REN: yes — Áreas Estratégicas de Proteção e Recarga de Aquíferos

Applicable regulation (read the full text — not interpreted here):
  Aviso n.º 1510/2022, DR 2.ª série, n.º 16  https://files.dre.pt/2s/2022/01/016000000/0032700390.pdf
  - Art. 31.º — Identificação e usos  [Espaços Centrais]
  - Art. 32.º — Espaços Centrais Nível 1 — regime de edificabilidade  [Espaços Centrais]
  - ...
  (full article text: --format json or markdown)

Sources:
  - DGT CRUS — ordenamento do solo (Tomar) [bundled-snapshot]
  - DGT SRUP — RAN (Tomar) [bundled-snapshot]
  - Município de Tomar (MuniSIG) — REN [bundled-snapshot]

Confidence: medium
```

The **regulation** section maps the matched zoning category to the *Regulamento*
articles that govern it (by matching the category to the regulation's sections) and
carries their **verbatim text** in the JSON/Markdown output — the raw rules for a
person or AI to read. It is retrieval, not interpretation.

Polygon output reports the analysed area and, per zoning category and per
constraint, the intersected area in m² and the percentage of the parcel.

### AI analysis report (`pdm analyse`)

`pdm analyse` is the one command that *does* call an AI: it runs the normal
query, hands the structured result (zoning, constraints, verbatim regulation
articles, confidence, and an explicit list of data gaps) to a Claude model, and
renders the returned pt-PT analysis into a self-contained report — viability
banner with executive summary, locator map, deterministic *Localização* facts,
a themed *Síntese* (identificação, enquadramento urbanístico, condicionantes,
acessos, morfologia, potencial construtivo, riscos, grau de incerteza, leitura
estratégica), and citations that deep-link to the verbatim articles.

```sh
export ANTHROPIC_API_KEY=sk-ant-...
pdm analyse 39.60 -8.41 > analysis.html                 # basic tier
pdm analyse ./parcel.geojson --tier premium > analysis.html
pdm analyse 39.60 -8.41 --format markdown               # or json
```

Two tiers (pricing follows the model behind each):

| tier | model | character |
|---|---|---|
| `basic` (default) | Claude Haiku 4.5 | fast, low-cost, grounded prose |
| `premium` | Claude Opus 4.8 (adaptive thinking) | deepest reasoning over the regulation articles and the strategic reading |

Grounding: the model receives *only* the query result and must write from it —
the schema-constrained output is post-validated in Go (citations to articles
not present in the data are dropped and disclosed), deterministic facts are
rendered from the query result rather than model text, and every report ends
with an AI-generation disclaimer on top of the standard one. Data gaps (e.g.
constraints not evaluated for a municipality) are computed deterministically
and rendered whether or not the model acknowledges them.

## Output format (JSON)

The JSON output (`--format json`) is the app's stable contract — the structured
**evidence pack** a downstream AI or program consumes. The canonical definition
is [`internal/model/model.go`](internal/model/model.go); this section is the
human-readable map of it. A `point` query returns one `PointResult`; a `polygon`
query returns one `PolygonResult` (same fields, minus the single input coordinate,
plus `analysed_area_m2` and per-hit `area_m2`/`percent`).

Top-level object (`PointResult`):

| field | type | meaning |
|---|---|---|
| `input` | `{lat, lon}` | the queried coordinate (WGS84 decimal degrees) |
| `municipality` | string | resolved concelho (CAOP) |
| `freguesia` | string | resolved freguesia (CAOP), if known |
| `supported` | bool | `true` if a municipality/plan was resolved and queried |
| `plan` | `PlanInfo` | the planning instrument in force (see below), if any |
| `zoning` | `[]ZoningHit` | zoning/land-use classes at the location |
| `constraints` | `[]ConstraintHit` | constraints checked, each with `present: true/false` |
| `regulation` | `Regulation` | applicable *Regulamento* articles (verbatim text), if retrieved |
| `sources` | `[]Source` | attribution + provenance for every layer used |
| `confidence` | enum | `high` \| `medium` \| `low` |
| `notes` | `[]string` | caveats (e.g. constraints not evaluated for this municipality) |
| `disclaimer` | string | the fixed legal disclaimer — always surface it |
| `generated_at` | RFC 3339 | when the result was produced |

Nested objects:

- **`plan`** (`PlanInfo`): `name`, `kind` (e.g. `PDM`), `municipality`,
  `published_ref` / `published_date` (Diário da República), and `documents[]`
  (`{title, url, note}`) linking the official plan/regulation.
- **`zoning[]`** (`ZoningHit`): `class` (e.g. `Solo Urbano`), `subclass`,
  `label` (combined human label), `raw_code` (raw source value), `layer`.
  For polygons: `area_m2`, `percent` of the parcel.
- **`constraints[]`** (`ConstraintHit`): `type` (`REN`, `RAN`, …), `label`,
  `present` (**the key field** — `false` means checked-and-absent, not
  unknown), `detail`, `layer`. For polygons: `area_m2`, `percent`.
- **`regulation`** (`Regulation`): `reference`, `url`, `note`, and `articles[]`
  (`{number, title, section, text}`) — the **verbatim** article bodies. These
  are *candidate* rules retrieved by matching the zoning category to regulation
  sections; the tool never interprets them.
- **`sources[]`** (`Source`): `name`, `layer`, `url`, `retrieved_at`, and
  `provenance` — one of `official-live`, `official-cache`, `bundled-snapshot`,
  or `sample` (illustrative only; downgrades confidence).

Two rules a consumer must respect: a constraint absent from `constraints[]`
is **not** the same as `present: false` (it means *not evaluated* — read
`notes` and `confidence`), and the `regulation.articles[].text` is raw source
material to reason over, never a computed building envelope.

## Data sources

All data is official and public. `pdm` prefers **vector** services (queryable
GeoJSON) over WMS/raster/PDF.

| Layer | Source | Service |
|---|---|---|
| Municipality boundaries (all mainland) | DGT **CAOP** (`municipios`) | OGC API Features |
| Freguesia boundaries (all mainland) | DGT **CAOP** (`freguesias`) | OGC API Features |
| Zoning, any mainland municipality (classificação e qualificação do solo) | DGT **CRUS** | OGC API Features |
| Rede Natura 2000 — ZPE + ZEC (national) | DGT/SNIT **SRUP** (`srup_zpe`, `srup_zec`) | OGC API Features |
| Perigosidade de incêndio rural, classes alta/muito alta (national) | DGT/SNIT **SRUP** (`srup_perigosidade_inc_rural`) | OGC API Features |
| RAN (Reserva Agrícola Nacional) — Tomar | DGT/SNIT **SRUP** | OGC API Features |
| REN (Reserva Ecológica Nacional) — Tomar | Município de Tomar / Médio Tejo (**MuniSIG**) | ArcGIS REST (GeoJSON) |
| Albufeira de Castelo de Bode (POACB area) — Tomar | Município de Tomar (**MuniSIG**), Zonas de Proteção e Salvaguarda | ArcGIS REST (GeoJSON) |
| Regulation articles (Regulamento) — Tomar | *Aviso n.º 1510/2022*, DR 2.ª série n.º 16 | [PDF](https://files.dre.pt/2s/2022/01/016000000/0032700390.pdf) → parsed into 103 articles with section context |
| Plan metadata | PCGT/DGT | [PCGT](https://pcgt.dgterritorio.gov.pt/FDE12471) |

Municipalities with a dedicated adapter run against a **bundled snapshot**
(embedded in the binary) by default, so they work offline and deterministically;
`--live` fetches fresh data from the services above (spatially filtered to the
query), caches it locally, and falls back to the bundle if a service is
unreachable — so public services are never hammered. Municipalities served by
the **generic adapter have no bundled snapshot**: their zoning is always fetched
live from CRUS (filtered to the municipality code and the query bbox) and
cached, `--live` or not.

### Regenerating the bundled snapshot

```sh
go run ./cmd/pdmdata             # refetch + rebuild everything under ./data
go run ./cmd/pdmdata ren         # just one layer
go run ./cmd/pdmdata regulamento # re-parse the Regulamento (needs `pdftotext`)
```

The ingest tool fetches from the official endpoints, reduces the (12–15 dp!)
coordinate precision to ~0.1 m, simplifies administrative boundaries used only for
resolution, filters attributes to the fields the tool uses, and writes compact
GeoJSON with a `_source` provenance block. The `regulamento` target downloads the
official PDM regulation PDF and parses it into articles (with chapter/section
context); it requires `pdftotext` (poppler) at regeneration time only.

## Architecture

```
cmd/pdm            CLI entry point
cmd/pdmdata        maintainer tool: regenerate bundled data from official services
internal/
  cli              argument parsing (negative-coordinate aware) + dispatch
  crs              WGS84 → ETRS89/PT-TM06 (EPSG:3763) projection for real m² areas
  spatial          GeoJSON loading + point-in-polygon + coverage (union of clips)
  admin            coordinate/polygon → municipality (CAOP boundaries)
  registry         municipality → adapter; "supported" set
  reg              Regulamento articles + zoning-category → article retrieval
  mapview          projects geometries to an inline SVG locator map
  adapter          per-municipality contract (Adapter interface)
    crus           shared DGT CRUS bits (live loader + feature classification)
    generic        fallback adapter: any mainland municipality, zoning via CRUS
    tomar          the pilot full adapter (zoning + RAN/REN/POACB + Regulamento)
  source           data loaders: bundled / file / WFS / OGC API Features + cache
  cache            on-disk cache with timestamps + TTL
  query            the engine: resolve → load layers → intersect → build result
  report           renderers: text / json / markdown
  model            domain types (results, hits, sources, confidence, disclaimer)
data               embedded bundled snapshot (boundaries + Tomar layers)
testdata           example parcels
```

Key design points:

- **Per-municipality adapters, generic fallback.** The query engine knows
  nothing about Tomar; it asks the adapter for the plan and the layers. Any
  municipality without a dedicated adapter is served by the generic CRUS
  adapter (zoning only). Upgrading one to full support is a new adapter + one
  `register(...)` line.
- **Real areas.** Areas/percentages are computed after projecting to
  ETRS89/PT-TM06 (metres), not in degrees.
- **Overlap-safe coverage.** Per-layer coverage is the *union* of clips, so
  overlapping features never double-count area.
- **Resilient to messy data.** Public polygons are often not OGC-valid; geometry
  is parsed with validation disabled and every overlay op is guarded so a single
  pathological feature degrades gracefully instead of failing the query.
- **Honest provenance & confidence.** Every source is labelled (`official-live`,
  `official-cache`, `bundled-snapshot`), and confidence is downgraded when a layer
  is missing or a match is absent.

### Upgrading a municipality to full support

Every mainland municipality already answers zoning via the generic adapter.
Full support adds its constraint layers and regulation:

1. Create `internal/adapter/<name>/` implementing `adapter.Adapter`
   (plan metadata, constraint layers — RAN/REN/servidões, Regulamento). Reuse
   `internal/adapter/crus` for the zoning layer.
2. `register(<name>.New())` in `internal/registry/registry.go`.
3. Add its layers to `cmd/pdmdata` and regenerate the bundled data.

## Development

```sh
go test ./...      # unit + offline integration tests
go vet ./...
gofmt -l .
```

## Limitations

- Only **Tomar** has full support (constraints + Regulamento); every other
  mainland municipality is **zoning-only** — its results carry an explicit note
  and low confidence, and missing constraints must not be read as absent ones.
- The **Azores and Madeira** are not covered (mainland DGT datasets only).
- Zoning-only municipalities require **network** on first query (live CRUS
  fetch, then cached).
- Bundled boundaries are simplified (~30 m) for resolution; near a municipal
  border the resolved municipality may be wrong.
- Beyond RAN/REN, other *servidões/restrições de utilidade pública* (protected
  areas, aquifers, aeronautical/road easements, …) are available from the same
  services and can be added as layers.
- Results are an automated approximation — see the disclaimer above.
