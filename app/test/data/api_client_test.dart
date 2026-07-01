import 'dart:async';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shopping_list/data/api/api_client.dart';

/// A fake [HttpClientAdapter] that captures the outgoing [RequestOptions]
/// instead of touching the network, and returns a canned 200 response.
///
/// This lets us assert on the headers Dio actually put on the wire — proving
/// the interceptor ran end-to-end — without any live HTTP.
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
  test('createDio attaches X-Device-Id to every outgoing request', () async {
    final dio = createDio(deviceId: 'dev-123');
    final adapter = _CapturingAdapter();
    dio.httpClientAdapter = adapter;

    await dio.get<void>('/anything');

    expect(
      adapter.captured?.headers['X-Device-Id'],
      equals('dev-123'),
      reason: 'the X-Device-Id interceptor must set the header on outgoing requests',
    );
  });
}
