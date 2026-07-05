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

Pilot municipality: **Tomar** (concelho 1418). Any other municipality is detected
and reported as *"detected but not yet supported"* — the architecture is
per-municipality adapters, so adding one is a small, contained change.

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
pdm supported                   list supported municipalities
pdm version
pdm help
```

Options:

| flag | meaning |
|---|---|
| `--format text\|json\|markdown` | output format (default `text`) |
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
```

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
| Municipality boundaries | DGT **CAOP** (`municipios`) | OGC API Features |
| Zoning (classificação e qualificação do solo) | DGT **CRUS** | OGC API Features |
| RAN (Reserva Agrícola Nacional) | DGT/SNIT **SRUP** | OGC API Features |
| REN (Reserva Ecológica Nacional) | Município de Tomar / Médio Tejo (**MuniSIG**) | ArcGIS REST (GeoJSON) |
| Regulation articles (Regulamento) | *Aviso n.º 1510/2022*, DR 2.ª série n.º 16 | [PDF](https://files.dre.pt/2s/2022/01/016000000/0032700390.pdf) → parsed into 103 articles with section context |
| Plan metadata | PCGT/DGT | [PCGT](https://pcgt.dgterritorio.gov.pt/FDE12471) |

By default `pdm` runs against a **bundled snapshot** of this data (embedded in the
binary), so it works offline and deterministically. `--live` fetches fresh data
from the services above (spatially filtered to the query), caches it locally, and
falls back to the bundle if a service is unreachable — so public services are
never hammered.

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
  adapter          per-municipality contract (Adapter interface)
    tomar          the pilot adapter
  source           data loaders: bundled / file / WFS / OGC API Features + cache
  cache            on-disk cache with timestamps + TTL
  query            the engine: resolve → load layers → intersect → build result
  report           renderers: text / json / markdown
  model            domain types (results, hits, sources, confidence, disclaimer)
data               embedded bundled snapshot (boundaries + Tomar layers)
testdata           example parcels
```

Key design points:

- **Per-municipality adapters.** The query engine knows nothing about Tomar; it
  asks the adapter for the plan and the layers. Adding a municipality is a new
  adapter + one `register(...)` line.
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

### Adding a municipality

1. Create `internal/adapter/<name>/` implementing `adapter.Adapter`
   (plan, layers, classification).
2. `register(<name>.New())` in `internal/registry/registry.go`.
3. Add its layers to `cmd/pdmdata` and regenerate the bundled data.

## Development

```sh
go test ./...      # unit + offline integration tests
go vet ./...
gofmt -l .
```

## Limitations

- Pilot covers **Tomar** only; other municipalities are detected but unsupported.
- Bundled boundaries are simplified for resolution; near a municipal border,
  prefer `--live`.
- The pilot ships zoning + RAN + REN. Other *servidões/restrições de utilidade
  pública* (protected areas, aquifers, aeronautical/road easements, …) are
  available from the same services and can be added as layers.
- Results are an automated approximation — see the disclaimer above.
