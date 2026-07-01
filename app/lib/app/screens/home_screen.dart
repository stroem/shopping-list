import 'package:flutter/material.dart';

/// The app shell's landing screen. A placeholder body for now — the feature
/// screens (lists, catalog, scanning) land in follow-up tickets (#14–#22).
class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      key: const ValueKey('home-screen'),
      appBar: AppBar(title: const Text('Handla')),
      body: const Center(child: Text('Your lists will appear here.')),
    );
  }
}
