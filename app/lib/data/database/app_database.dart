import 'package:drift/drift.dart';

part 'app_database.g.dart';

/// A shopping list. Named `ShoppingList` to avoid clashing with
/// `dart:core.List`; mirrors the server `lists` table.
@DataClassName('ShoppingList')
class Lists extends Table {
  TextColumn get id => text()();
  TextColumn get name => text()();
  DateTimeColumn get archivedAt => dateTime().nullable()();
  DateTimeColumn get createdAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get updatedAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get deletedAt => dateTime().nullable()();

  @override
  Set<Column> get primaryKey => {id};
}

/// Per-household product master driving autocomplete; mirrors `items`.
class Items extends Table {
  TextColumn get id => text()();
  TextColumn get name => text()();
  IntColumn get aisle => integer().nullable()();
  TextColumn get imageUrl => text().nullable()();
  TextColumn get source => text().nullable()();
  TextColumn get externalId => text().nullable()();
  IntColumn get purchaseCount => integer().withDefault(const Constant(0))();
  DateTimeColumn get lastPurchasedAt => dateTime().nullable()();
  DateTimeColumn get createdAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get updatedAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get deletedAt => dateTime().nullable()();

  @override
  Set<Column> get primaryKey => {id};
}

/// One occurrence of a product on a list; mirrors `list_items`.
class ListItems extends Table {
  TextColumn get id => text()();
  TextColumn get listId => text()();
  TextColumn get itemId => text().nullable()();
  TextColumn get name => text()();
  IntColumn get quantity => integer().withDefault(const Constant(1))();
  TextColumn get note => text().nullable()();
  IntColumn get aisle => integer().nullable()();
  IntColumn get position => integer().withDefault(const Constant(0))();
  DateTimeColumn get checkedAt => dateTime().nullable()();
  TextColumn get checkedBy => text().nullable()();
  DateTimeColumn get createdAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get updatedAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get deletedAt => dateTime().nullable()();

  @override
  Set<Column> get primaryKey => {id};
}

/// Append-only history of a list item being ticked off; mirrors
/// `check_off_events`.
class CheckOffEvents extends Table {
  TextColumn get id => text()();
  TextColumn get listItemId => text().nullable()();
  TextColumn get userId => text().nullable()();
  TextColumn get itemId => text().nullable()();
  TextColumn get storeId => text().nullable()();
  IntColumn get quantity => integer().withDefault(const Constant(1))();
  DateTimeColumn get checkedAt => dateTime()();
  DateTimeColumn get createdAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get updatedAt =>
      dateTime().clientDefault(() => DateTime.now())();
  DateTimeColumn get deletedAt => dateTime().nullable()();

  @override
  Set<Column> get primaryKey => {id};
}

/// Local-first Drift database mirroring the four server entities. All tables
/// carry `updatedAt` + `deletedAt` for pull sync (soft delete).
@DriftDatabase(tables: [Lists, Items, ListItems, CheckOffEvents])
class AppDatabase extends _$AppDatabase {
  AppDatabase(super.e);

  @override
  int get schemaVersion => 1;
}
