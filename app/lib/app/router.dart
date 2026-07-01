import 'package:go_router/go_router.dart';

import 'screens/home_screen.dart';

/// Builds the app's [GoRouter]. A single route for now — the shell renders
/// [HomeScreen] at the root; feature routes are added in follow-up tickets.
GoRouter createRouter() {
  return GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(path: '/', builder: (context, state) => const HomeScreen()),
    ],
  );
}
