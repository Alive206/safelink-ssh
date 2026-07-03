import 'package:flutter_test/flutter_test.dart';

import 'package:safelink_android/main.dart';

void main() {
  testWidgets('SafeLink shell renders primary navigation', (tester) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    expect(find.text('SafeLink'), findsOneWidget);
    expect(find.text('首页'), findsWidgets);
    expect(find.text('节点'), findsWidgets);
    expect(find.text('订阅'), findsWidgets);
    expect(find.text('隧道'), findsWidgets);
  });
}
