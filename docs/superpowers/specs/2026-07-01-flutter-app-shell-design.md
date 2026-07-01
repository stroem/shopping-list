# Flutter app shell — design (#13)

## Goal
Stand up the Flutter app foundation that every other M3 app issue (#14–#22) hangs
off: a Riverpod provider graph, `go_router` navigation, a Drift/SQLite local
schema mirroring the server entities for local-first reads, and a Dio HTTP client
that sends `X-Device-Id`. It must run on web (`-d chrome`) and stay Android-ready.

## Acceptance criteria
- Riverpod provider graph is in place: a `ProviderScope` root and providers for
  the database, HTTP client, device id, and router that resolve without error.
- `go_router` drives navigation with at least a home route rendered through
  `MaterialApp.router`.
- Drift/SQLite schema mirrors the server entities `lists`, `items`, `list_items`,
  and `check_off_events` (including the `updated_at` / `deleted_at` sync columns),
  and supports local-first reads (insert + query round-trips in-memory).
- A Dio HTTP client attaches the `X-Device-Id` header to every outgoing request.
- App builds/runs on web and stays Android-ready (no desktop/native-only APIs on
  the hot path; Drift uses the cross-platform `drift_flutter` opener).

## Assumptions
- **Riverpod without codegen.** Use `flutter_riverpod` with hand-written
  `Provider`s rather than `riverpod_generator`. Satisfies "Riverpod provider
  graph" while removing one codegen layer (only Drift needs `build_runner`),
  reducing flakiness. `@riverpod` codegen can be introduced later if desired.
- **Drift cross-platform opener.** Use `drift_flutter`'s `driftDatabase(name:)`
  which selects a native connection on Android and a WASM/IndexedDB connection on
  web, keeping both targets working from one call. Tests use
  `NativeDatabase.memory()` so they run on the Dart VM without assets.
- **Local schema is a mirror, not a 1:1 copy.** Only the four food-list entities
  the issue names (`lists`, `items`, `list_items`, `check_off_events`) are
  mirrored now; stores/aisles and catalog tables are out of scope for the shell
  and land with their own tickets. IDs are stored as `text` (server sends UUIDs as
  strings); timestamps as `DateTime`.
- **Device id.** Generated once (UUID v4) and persisted via `shared_preferences`;
  exposed through a provider. The Dio factory takes the id as a plain string so
  the interceptor is unit-testable without platform plugins.
- **Home route only.** The shell ships a single placeholder Home screen; feature
  screens (lists, scan, stats) arrive with #14–#22. Routing is structured so
  adding routes is a one-line change.
- **No network calls yet.** The Dio client is configured (base URL from a
  const/env-style default, `X-Device-Id` interceptor) but the shell makes no live
  requests; pull-sync/outbox wiring is a later ticket.

## Approach
- Add dependencies: `flutter_riverpod`, `go_router`, `dio`, `drift`,
  `drift_flutter`, `sqlite3_flutter_libs`, `path_provider`, `uuid`,
  `shared_preferences`; dev `build_runner`, `drift_dev`.
- `lib/data/database/app_database.dart` — Drift `@DriftDatabase` with four tables
  (`Lists`, `Items`, `ListItems`, `CheckOffEvents`) mirroring the server columns;
  generated `app_database.g.dart` via `build_runner`. A `AppDatabase` ctor takes a
  `QueryExecutor` so tests inject `NativeDatabase.memory()`.
- `lib/data/api/api_client.dart` — `createDio({required String deviceId, String baseUrl})`
  returning a `Dio` with an interceptor that sets the `X-Device-Id` header.
- `lib/data/device_id.dart` — load-or-create the persisted device id.
- `lib/app/providers.dart` — `appDatabaseProvider`, `apiClientProvider`,
  `deviceIdProvider`, `routerProvider`.
- `lib/app/router.dart` + `lib/app/screens/home_screen.dart` — `go_router` config
  with the home route.
- `lib/app/app.dart` + `lib/main.dart` — `ProviderScope` → `MaterialApp.router`.

## Out of scope
- Live pull-sync, the outbox write queue, and OIDC/Google sign-in (later tickets).
- Store/aisle/catalog local tables and any aisle-sorting logic.
- Alcohol / non-food anything; realtime push. (Repo scope boundaries.)
