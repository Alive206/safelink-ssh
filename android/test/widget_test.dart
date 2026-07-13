import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:safelink_android/main.dart';

void main() {
  testWidgets('SafeLink shell renders primary navigation', (tester) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    expect(find.text('SafeLink'), findsOneWidget);
    expect(find.text('首页'), findsWidgets);
    expect(find.text('节点'), findsWidgets);
    expect(find.text('我的'), findsWidgets);
    expect(find.text('节点列表'), findsOneWidget);
    expect(find.text('断开连接'), findsNothing);
  });

  testWidgets('SafeLink shell shows my page cards', (tester) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    await tester.tap(find.text('我的').last);
    await tester.pumpAndSettle();

    expect(find.text('订阅管理'), findsOneWidget);
    expect(find.text('常用功能'), findsOneWidget);
    await tester.drag(find.byType(ListView), const Offset(0, -500));
    await tester.pumpAndSettle();
    expect(find.text('连接日志'), findsOneWidget);
  });
}
