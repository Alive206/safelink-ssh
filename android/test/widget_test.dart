import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:safelink_android/main.dart';

Future<void> openTab(WidgetTester tester, String label) async {
  await tester.pumpWidget(const SafeLinkApp());
  await tester.pump();
  await tester.tap(find.text(label).last);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('SafeLink shell renders primary navigation', (tester) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    expect(find.text('SafeLink'), findsOneWidget);
    expect(find.text('首页'), findsWidgets);
    expect(find.text('数据'), findsWidgets);
    expect(find.text('设置'), findsWidgets);
    expect(find.text('配置'), findsNothing);
  });

  testWidgets('home page shows shadowrocket style status card', (
    tester,
  ) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    expect(find.text('未连接'), findsOneWidget);
    expect(find.byType(Switch), findsOneWidget);
    expect(find.text('路由模式'), findsOneWidget);
    expect(find.text('连通性测试'), findsOneWidget);
    expect(find.text('暂无订阅'), findsOneWidget);
    expect(find.text('点击订阅可以折叠和展开子节点。圆点代表默认节点。'), findsOneWidget);
  });

  testWidgets('proxy mode sheet opens from status card', (tester) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    await tester.tap(find.text('路由模式'));
    await tester.pumpAndSettle();

    expect(find.text('规则模式'), findsWidgets);
    expect(find.text('全局模式'), findsOneWidget);
    expect(find.text('直连模式'), findsOneWidget);
  });

  testWidgets('add subscription dialog asks only for url', (tester) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    await tester.tap(find.byIcon(Icons.add));
    await tester.pumpAndSettle();

    expect(find.text('从给定的 URL 下载配置…'), findsOneWidget);
    expect(find.byType(TextField), findsOneWidget);
    expect(find.text('名称'), findsNothing);
    expect(find.text('取消'), findsOneWidget);
    expect(find.text('下载'), findsOneWidget);
  });

  testWidgets('app bar offers qr scan import', (tester) async {
    await tester.pumpWidget(const SafeLinkApp());
    await tester.pump();

    expect(find.byIcon(Icons.qr_code_scanner), findsOneWidget);
    expect(find.byIcon(Icons.content_paste_outlined), findsNothing);
  });

  testWidgets('data tab shows traffic stats and logs', (tester) async {
    await openTab(tester, '数据');

    expect(find.text('上传速度'), findsOneWidget);
    expect(find.text('下载速度'), findsOneWidget);
    expect(find.text('上传流量'), findsOneWidget);
    expect(find.text('下载流量'), findsOneWidget);
    await tester.drag(find.byType(ListView), const Offset(0, -400));
    await tester.pumpAndSettle();
    expect(find.text('连接日志'), findsOneWidget);
  });

  testWidgets('settings tab switches theme mode', (tester) async {
    await openTab(tester, '设置');

    expect(find.text('外观'), findsOneWidget);
    expect(find.text('订阅证书'), findsOneWidget);
    expect(find.text('分享应用'), findsNothing);

    await tester.tap(find.byType(DropdownButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('深色').last);
    await tester.pumpAndSettle();

    final app = tester.widget<MaterialApp>(find.byType(MaterialApp));
    expect(app.themeMode, ThemeMode.dark);
  });
}
