# Handla — den perfekta matinköpslistan (vision + v1-design)

**Status:** vision / design, godkänd 2026-06-28
**Föregångare:** `../shopping` ("handla", Flutter + Firebase) och `../shopping_v2`
(Flutter + Go + Postgres, Systembolaget-fokus). Båda blev för breda. Detta repo
gör om — **bara mat, gjort perfekt.**

## 1. Vision

En enda sak, gjord perfekt: **en delad inköpslista för mat som lär sig vad
hushållet faktiskt köper och är sorterad efter butikens gångordning**, så att
handlingen går snabbt. Inget mer.

Två personer (du + sambo) delar listor. Appen är offline-först och känns snabb
även utan nät. Driftkostnaden ska i praktiken vara ~0 kr.

## 2. Omfattning

### Med i v1
1. **Delade matlistor** — ett hushåll (du + sambo) via en delad hushållskod.
   Offline-först; synkar när nät finns.
2. **Smart autocomplete** — fritext rankad efter vad ni brukar köpa
   (`purchase_count` + `last_purchased_at`). Den vassaste funktionen från
   `handla`.
3. **Bocka av → historik** — avbockade varor flyttas till historik med vem/när.
4. **Gångordning** — listan sorteras efter butikssektion (mejeri, frukt, …);
   drag-and-drop för att anpassa ordningen.
5. **Antal + notis** per vara.
6. **Matkatalog-stöd (lätt):** Livsmedelsverkets generiska livsmedel för förslag
   + **streckkodsskanning mot Open Food Facts** via förimporterade EAN-mappningar
   (ingen tung katalog-nedladdning i appen).
7. **Lätt statistik** — mest köpta varor.

### Medvetet bortskuret (lärdomar från v1/v2)
Alkohol / Systembolaget · kategorierna Hem & Present · push-notiser (ersätts av
pull-sync) · realtids-Firestore · stora katalog-nedladdningar i appen · betyg &
favoriter. Detta är exakt det som gjorde de tidigare försöken för stora. Scope
creep tillbaka in i något av detta avvisas medvetet.

## 3. Arkitektur

```
Flutter-app  (web + Android först, iOS-redo)
  Riverpod · Drift/SQLite lokalt · outbox för skrivningar
        │  HTTPS / JSON
        ▼
API Gateway (HTTP API — billigare än REST API)
        │
  Go-backend som AWS Lambda  (en binär, chi via lambda-adapter)
        │  pgx + Neon connection pooler
   Neon Postgres  (scale-to-zero → ~0 kr i vila)
        ▲
  EventBridge-schemalagd Lambda → veckovis matkatalog-refresh (valfri)
```

- **Samma handlers lokalt och i Lambda.** `cmd/api` kör en vanlig HTTP-server för
  lokal utveckling; `cmd/lambda` wrappar exakt samma router för API Gateway. Lokal
  dev kräver aldrig AWS.
- **Kostnadsprincip (hård):** allt ska skala till noll eller ligga i free tier.
  HTTP API > REST API GW. Schemalagd Lambda > always-on cron. Neon pausar vid
  idle. Inga always-on-containrar.

## 4. Datamodell (Postgres)

Alla tabeller har `updated_at` + `deleted_at` (soft delete) för pull-sync.

| Tabell | Nyckelfält | Syfte |
|---|---|---|
| `households` | `id (uuid)`, `name`, `created_at` | Delad enhet; `id` är join-koden |
| `users` | `id`, `device_id (unik)`, `display_name`, `household_id` | Device-baserad identitet |
| `lists` | `id (uuid)`, `household_id`, `name`, `archived_at` | Inköpslistor |
| `list_items` | `id (uuid)`, `list_id`, `item_id`, `name`, `quantity`, `note`, `aisle`, `position`, `checked_at`, `checked_by` | Rader på en lista |
| `items` | `id`, `household_id`, `name`, `aisle`, `purchase_count`, `last_purchased_at` | Produktmaster per hushåll (driver autocomplete) |
| `check_off_events` | `id`, `list_item_id`, `user_id`, `item_id`, `quantity`, `checked_at` | Append-only historik → statistik |
| `food_catalog` | `id`, `source`, `name`, `huvudgrupp`, `aisle` | Livsmedelsverkets generiska livsmedel |
| `ean_mappings` | `ean (pk)`, `name`, `brand`, `aisle`, `source` | Streckkod → produkt (Open Food Facts) |

## 5. Sync & delning

- **Lokal-först:** appen läser alltid från Drift/SQLite. Skrivningar sker lokalt
  först och läggs i en **outbox** som spelas upp mot backend (vid app-start, vid
  återställd anslutning, med exponentiell backoff).
- **Pull-sync:** `GET …?since=<updated_at-cursor>` hämtar ändringar. Ingen
  realtids-push i v1 (kostnad/komplexitet). Push (API Gateway WebSocket / SNS) är
  en framtida ticket.
- **Idempotens:** klient-genererade UUID:n + `Idempotency-Key` så dubbeltryck och
  outbox-replays inte skapar dubbletter.
- **Delning / auth:** hushåll = delad hemlig UUID (join-kod) + per-enhet
  `X-Device-Id`. Ingen OAuth i v1. Allt är hushållsskopat; korsåtkomst svarar
  **404** (läcker inte existens). Riktig inloggning är en framtida ticket.

## 6. Datapipeline (`data/` → register)

`data/` är **gitignorad och committas aldrig** — det är källmaterial, inte kod.
En engångs-importer `backend/cmd/seed` läser `data/food/` och fyller Postgres:

- `livsmedelsverket_products.json` (~2 575 generiska livsmedel) → `food_catalog`
  (namn + `huvudgrupp` → `aisle`-mappning) för autocomplete-förslag.
- `swedish_food_products.jsonl` (~24 940 Open Food Facts-poster) → `ean_mappings`
  (filtrera till poster med både namn och `code`/EAN; kategori → aisle) för
  streckkodsskanning.
- `livsmedelsverket_details.json` (näringsdata) används inte i v1.

Importen körs lokalt/en gång; rådatan distribueras aldrig med appen eller repot.

## 7. Kostnadsmodell

Vid hushållsskala: Lambda + HTTP API ligger i free tier; Neon pausar vid idle ⇒
i praktiken ~0 kr/månad. Inga always-on-resurser. Detta är en designinvariant,
inte en optimering på efterhand.

## 8. Teststrategi

- **Backend (Go):** enhetstester + integrationstester mot Postgres
  (testcontainers eller en `DATABASE_URL`); DB-beroende tester *skippar rent* när
  databasen saknas så CI inte bryts.
- **App (Flutter):** enhets- + widgettester (Riverpod/Drift mockas).
- Grön bar = `go test ./...` (backend) + `flutter test` (app).

## 9. Vad som händer härnäst

Detta dokument är visionen. Konkret arbete bryts ner i GitHub-issues i
`stroem/shopping-list` (se den föreslagna ticket-listan) och drivs via
`/create-issue` → `/auto`. GitHub-issuet är delat minne för de stateless
agenterna; durabla beslut skrivs där och i `AGENTS.md`, inte i chatten.
