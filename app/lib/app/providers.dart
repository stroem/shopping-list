import 'package:dio/dio.dart';
import 'package:drift/drift.dart';
import 'package:drift_flutter/drift_flutter.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/api/api_client.dart';
import '../data/database/app_database.dart';

/// The app's device id (`X-Device-Id`). Overridden at app start with the value
/// from [loadOrCreateDeviceId], and in tests via [Provider.overrideWithValue].
final deviceIdProvider = Provider<String>(
  (ref) => throw UnimplementedError(
    'deviceIdProvider must be overridden at app start',
  ),
);

/// The local-first Drift database, opened with the cross-platform (web +
/// Android) [driftDatabase] opener and closed when the provider is disposed.
///
/// The opener is wrapped in a [LazyDatabase] so the underlying connection is
/// only established on first query — constructing the provider stays a cheap,
/// side-effect-free operation.
final appDatabaseProvider = Provider<AppDatabase>((ref) {
  final db = AppDatabase(LazyDatabase(() => driftDatabase(name: 'handla')));
  ref.onDispose(db.close);
  return db;
});

/// The app's [Dio] client, stamping the resolved [deviceIdProvider] onto every
/// request via [createDio].
final apiClientProvider = Provider<Dio>(
  (ref) => createDio(deviceId: ref.watch(deviceIdProvider)),
);
