import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shopping_list/app/app.dart';
import 'package:shopping_list/app/providers.dart';

void main() {
  testWidgets('HandlaApp renders the Home screen at the initial route', (
    tester,
  ) async {
    // deviceIdProvider throws unless overridden; the rest of the graph —
    // database, api client, router — resolves off that override.
    await tester.pumpWidget(
      ProviderScope(
        overrides: [deviceIdProvider.overrideWithValue('test-device')],
        child: const HandlaApp(),
      ),
    );
    await tester.pumpAndSettle();

    expect(
      find.byKey(const ValueKey('home-screen')),
      findsOneWidget,
      reason:
          'the go_router initial route must render HomeScreen, keyed with '
          "ValueKey('home-screen'), through MaterialApp.router",
    );
    expect(
      find.text('Handla'),
      findsWidgets,
      reason: "the Home screen's AppBar must show the 'Handla' title",
    );
  });
}
