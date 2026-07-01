import 'dart:async';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shopping_list/app/providers.dart';
import 'package:shopping_list/data/api/api_client.dart';
import 'package:shopping_list/data/database/app_database.dart';

/// A fake [HttpClientAdapter] that captures the outgoing [RequestOptions]
/// instead of touching the network, and returns a canned 200 response.
///
/// The `X-Device-Id` header is stamped by an interceptor (not on
/// `BaseOptions`), so the only faithful way to prove the graph threaded the
/// overridden device id all the way into the client is to drive a request and
/// inspect what Dio actually put on the wire — no live HTTP required.
class _CapturingAdapter implements HttpClientAdapter {
  RequestOptions? captured;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    captured = options;
    return ResponseBody.fromString('{}', 200);
  }

  @override
  void close({bool force = false}) {}
}

void main() {
  test('provider graph resolves appDatabaseProvider to an AppDatabase', () {
    final container = ProviderContainer(
      overrides: [deviceIdProvider.overrideWithValue('dev-xyz')],
    );
    addTearDown(container.dispose);

    final db = container.read(appDatabaseProvider);

    expect(db, isA<AppDatabase>());
  });

  test(
    'apiClientProvider builds a Dio that stamps the overridden device id',
    () async {
      final container = ProviderContainer(
        overrides: [deviceIdProvider.overrideWithValue('dev-xyz')],
      );
      addTearDown(container.dispose);

      final dio = container.read(apiClientProvider);
      expect(dio, isA<Dio>());

      final adapter = _CapturingAdapter();
      dio.httpClientAdapter = adapter;
      await dio.get<void>('/anything');

      expect(
        adapter.captured?.headers[deviceIdHeader],
        equals('dev-xyz'),
        reason:
            'apiClientProvider must build createDio(deviceId: ref.watch('
            'deviceIdProvider)) so the overridden id reaches the wire',
      );
    },
  );
}
