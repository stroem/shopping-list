// RED test for Task 1 (#13): the Drift local schema.
//
// Pins the contract that `AppDatabase` mirrors the four server entities
// (`lists`, `items`, `list_items`, `check_off_events`), each carrying the
// `updated_at` / `deleted_at` sync columns, and supports local-first reads
// (insert + query round-trip) on an injected in-memory executor.
//
// Assumed production contract (implementer must match):
//   * ctor:            AppDatabase(QueryExecutor e)
//   * library:         package:shopping_list/data/database/app_database.dart
//   * table getters:   db.lists, db.items, db.listItems, db.checkOffEvents
//                      (Drift camelCases the Dart table class names
//                       Lists / Items / ListItems / CheckOffEvents)
//   * lists columns:   id (text), name (text), createdAt, updatedAt,
//                      deletedAt (nullable), archivedAt (nullable)
//   * sync columns:    every table exposes updatedAt + deletedAt.

import 'package:drift/drift.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shopping_list/data/database/app_database.dart';

void main() {
  late AppDatabase db;

  setUp(() {
    // In-memory executor: runs on the Dart VM, no platform plugins/assets.
    db = AppDatabase(NativeDatabase.memory());
  });

  tearDown(() async {
    await db.close();
  });

  test('lists row round-trips through the local schema', () async {
    final now = DateTime.utc(2026, 7, 1, 12);

    await db.into(db.lists).insert(
          ListsCompanion(
            id: const Value('list-1'),
            name: const Value('Groceries'),
            createdAt: Value(now),
            updatedAt: Value(now),
          ),
        );

    final row = await db.select(db.lists).getSingle();

    expect(row.name, 'Groceries');
  });

  test('lists table exposes updated_at + deleted_at sync columns', () {
    expect(db.lists.updatedAt, isA<GeneratedColumn>());
    expect(db.lists.deletedAt, isA<GeneratedColumn>());
  });

  test('items table exposes updated_at + deleted_at sync columns', () {
    expect(db.items.updatedAt, isA<GeneratedColumn>());
    expect(db.items.deletedAt, isA<GeneratedColumn>());
  });

  test('list_items table exposes updated_at + deleted_at sync columns', () {
    expect(db.listItems.updatedAt, isA<GeneratedColumn>());
    expect(db.listItems.deletedAt, isA<GeneratedColumn>());
  });

  test('check_off_events table exposes updated_at + deleted_at sync columns',
      () {
    expect(db.checkOffEvents.updatedAt, isA<GeneratedColumn>());
    expect(db.checkOffEvents.deletedAt, isA<GeneratedColumn>());
  });
}
