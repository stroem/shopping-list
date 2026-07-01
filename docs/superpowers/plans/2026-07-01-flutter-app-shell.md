# Flutter app shell — plan (#13)

## Goal / Architecture / Constraints
Foundation for M3 app: Riverpod graph, `go_router`, Drift local schema mirroring
`lists`/`items`/`list_items`/`check_off_events`, Dio client sending `X-Device-Id`.
Runs on web + Android. Local-first (UI reads Drift). Riverpod without codegen;
only Drift uses `build_runner`. Cost invariant unaffected (client-only). Green bar
and canonical Flutter commands: see `AGENTS.md` (`flutter test`, `build_runner`).

Prereq (orchestrator scaffolding, non-behaviour `chore` commit): add dependencies
to `pubspec.yaml`, `flutter pub get`, exclude `*.g.dart` from analysis.

## Tasks (TDD order)
1. [task] Drift schema mirrors the four server entities — files:
   `app/lib/data/database/app_database.dart` (+ generated `app_database.g.dart`);
   test: `app/test/data/app_database_test.dart`; depends-on: none;
   test proves: an in-memory `AppDatabase` inserts a `lists` row and reads it back,
   and the schema exposes `items`, `list_items`, `check_off_events` tables each
   carrying `updated_at` + `deleted_at`.

2. [task] Dio client attaches `X-Device-Id` — files:
   `app/lib/data/api/api_client.dart`; test: `app/test/data/api_client_test.dart`;
   depends-on: none;
   test proves: `createDio(deviceId: 'dev-123')` produces a `Dio` whose outgoing
   request options carry header `X-Device-Id: dev-123` (via the interceptor).

3. [task] Riverpod provider graph resolves — files: `app/lib/app/providers.dart`,
   `app/lib/data/device_id.dart`; test: `app/test/app/providers_test.dart`;
   depends-on: 1,2;
   test proves: a `ProviderContainer` (with `deviceIdProvider` overridden) resolves
   `appDatabaseProvider` to an `AppDatabase`, `apiClientProvider` to a `Dio` whose
   default `X-Device-Id` header equals the overridden id, without throwing.

4. [task] go_router shell renders Home under ProviderScope — files:
   `app/lib/app/router.dart`, `app/lib/app/screens/home_screen.dart`,
   `app/lib/app/app.dart`, `app/lib/main.dart`; test:
   `app/test/app/app_test.dart` (replaces the counter `widget_test.dart`);
   depends-on: 3;
   test proves: pumping the app inside a `ProviderScope` renders the Home screen
   (a known key/text) via `MaterialApp.router` + `go_router`.

## Parallelism
- Tasks 1 and 2 are independent (disjoint files) → run their `tester`s together,
  then their `implementer`s together, then commit sequentially (1 then 2).
- Task 3 depends on 1,2; task 4 depends on 3 → run strictly in order.

## Affected packages
- `app/lib/data/…` — Drift database + Dio client + device id.
- `app/lib/app/…` — providers, router, screens, app root.
- `app/lib/main.dart` — ProviderScope + MaterialApp.router.
- `app/pubspec.yaml` — new dependencies.
