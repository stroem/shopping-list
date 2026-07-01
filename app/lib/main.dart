import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'app/app.dart';
import 'app/providers.dart';
import 'data/device_id.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final deviceId = await loadOrCreateDeviceId();
  runApp(
    ProviderScope(
      overrides: [deviceIdProvider.overrideWithValue(deviceId)],
      child: const HandlaApp(),
    ),
  );
}
