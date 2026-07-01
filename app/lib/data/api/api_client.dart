import 'package:dio/dio.dart';

/// Header carrying the app's device id on every request (see AGENTS.md).
const deviceIdHeader = 'X-Device-Id';

/// Builds the app's [Dio] client, pointed at [baseUrl] and stamping
/// [deviceId] onto the [deviceIdHeader] of every outgoing request.
Dio createDio({
  required String deviceId,
  String baseUrl = 'http://localhost:8080',
}) {
  final dio = Dio(
    BaseOptions(
      baseUrl: baseUrl,
      contentType: Headers.jsonContentType,
    ),
  );

  dio.interceptors.add(
    InterceptorsWrapper(
      onRequest: (options, handler) {
        options.headers[deviceIdHeader] = deviceId;
        handler.next(options);
      },
    ),
  );

  return dio;
}
