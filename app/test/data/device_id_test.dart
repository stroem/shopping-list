import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:shopping_list/data/device_id.dart';

/// Coverage for [loadOrCreateDeviceId] — the persist-or-generate branch that
/// gives each install a stable [deviceIdPrefsKey] value. Exercises both arms of
/// the conditional: generate-and-persist on first launch, and return-existing
/// on every launch thereafter.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('generates a non-empty id and persists it under the key on first call',
      () async {
    final prefs = await SharedPreferences.getInstance();

    final id = await loadOrCreateDeviceId(prefs);

    expect(id, isNotEmpty);
    // The generate branch must write through: the returned id is exactly what
    // a fresh read of the store yields.
    expect(prefs.getString(deviceIdPrefsKey), id);
  });

  test('returns the same id on a second call (stable across calls)', () async {
    final prefs = await SharedPreferences.getInstance();

    final first = await loadOrCreateDeviceId(prefs);
    final second = await loadOrCreateDeviceId(prefs);

    expect(second, first);
  });

  test('returns the pre-existing id unchanged when prefs already hold one',
      () async {
    SharedPreferences.setMockInitialValues({
      deviceIdPrefsKey: 'preseeded-id',
    });
    final prefs = await SharedPreferences.getInstance();

    final id = await loadOrCreateDeviceId(prefs);

    // The return-existing branch: no regeneration, no overwrite.
    expect(id, 'preseeded-id');
    expect(prefs.getString(deviceIdPrefsKey), 'preseeded-id');
  });
}
