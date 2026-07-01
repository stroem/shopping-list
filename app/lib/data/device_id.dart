import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

/// Key under which the persisted device id is stored.
const deviceIdPrefsKey = 'device_id';

/// Loads the persisted device id, or generates and persists a fresh v4 UUID on
/// first launch. Platform-safe for web + Android via `shared_preferences`.
///
/// Pass [prefs] to reuse an existing instance (or a fake in tests); when
/// omitted the shared instance is obtained on demand.
Future<String> loadOrCreateDeviceId([SharedPreferences? prefs]) async {
  final store = prefs ?? await SharedPreferences.getInstance();
  final existing = store.getString(deviceIdPrefsKey);
  if (existing != null) {
    return existing;
  }

  final deviceId = const Uuid().v4();
  await store.setString(deviceIdPrefsKey, deviceId);
  return deviceId;
}
