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

**All mainland municipalities are supported for zoning.** Any coordinate/parcel
in continental Portugal resolves to its municipality (bundled CAOP boundaries)
and gets its zoning from the national **DGT CRUS** dataset — the harmonised
carta of every municipality's plans in force — fetched live (bbox-filtered) and
cached.

**All mainland municipalities also get the national constraint layers**,
evaluated as server-side presence probes (the underlying polygons — whole-
municipality REN delimitations, whole-park boundaries — are far too large to
download per query, so the services are asked what intersects the subject and
only attributes come back):

- **RAN** and **REN** from DGT/SNIT SRUP (REN exclusion polygons are
  subtracted; municipalities missing from the national dataset answer
  **unknown**, never "no" — ~50 lack their REN in SNIT, 6 lack a published RAN,
  and Lisboa/Porto/Amadora genuinely have no RAN);
- **Rede Natura 2000** (ZEC + ZPE) and **áreas protegidas (RNAP)** from the
  same catalogue;
- **albufeiras classificadas** (in-water and the statutory DL 107/2009
  protection belt, via true server-side distance queries) and the coastal
  **POC/POOC** areas, safeguard strips, digitized **POAAP** zonings and
  **PAAP** areas from APA/SNIAmb ArcGIS services.

On top of the live layers, every result lists the **special planning
instruments** (planos/programas especiais — POAAP/PEAAP, POOC/POC, POE,
POAP/PEAP) touching the municipality, from a bundled registry of ~121
instruments compiled from the official APA/ICNF/DGT/DRE registries (statuses
as of July 2026), with a positive "the queried location falls inside its area"
marker whenever a live layer confirms it.

Two support levels remain:

- **Full** (dedicated adapter): bundled snapshot layers **+ Regulamento
  articles**. Pilot: **Tomar** (concelho 1418).
- **Generic**: every other mainland municipality — live CRUS zoning + the
  national constraint layers above, at **medium confidence**, with an explicit
  note that municipality-specific condicionantes and the written regulation are
  not yet integrated.

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
pdm supported                   show municipality coverage/support levels
pdm version
pdm help
```

Options:

| flag | meaning |
|---|---|
| `--format text\|json\|markdown\|html` | output format (default `text`) |
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

## Data sources

All data is official and public. `pdm` prefers **vector** services (queryable
GeoJSON) over WMS/raster/PDF.

| Layer | Source | Service |
|---|---|---|
| Municipality boundaries (all mainland) | DGT **CAOP** (`municipios`) | OGC API Features |
| Zoning, any mainland municipality (classificação e qualificação do solo) | DGT **CRUS** | OGC API Features |
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
