import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

void main() {
  runApp(const SafeLinkApp());
}

class SafeLinkApp extends StatefulWidget {
  const SafeLinkApp({super.key});

  @override
  State<SafeLinkApp> createState() => _SafeLinkAppState();
}

class _SafeLinkAppState extends State<SafeLinkApp> {
  late final NativeBridge native;
  late final AppController controller;

  @override
  void initState() {
    super.initState();
    native = NativeBridge();
    controller = AppController(native)..load();
  }

  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: controller,
      builder: (context, _) {
        return MaterialApp(
          title: 'SafeLink',
          debugShowCheckedModeBanner: false,
          themeMode: switch (controller.themeMode) {
            'light' => ThemeMode.light,
            'dark' => ThemeMode.dark,
            _ => ThemeMode.system,
          },
          theme: buildAppTheme(Brightness.light),
          darkTheme: buildAppTheme(Brightness.dark),
          home: SafeLinkShell(controller: controller),
        );
      },
    );
  }
}

ThemeData buildAppTheme(Brightness brightness) {
  final scheme = ColorScheme.fromSeed(
    seedColor: const Color(0xff4a7dbd),
    brightness: brightness,
  );
  return ThemeData(
    colorScheme: scheme,
    useMaterial3: true,
    scaffoldBackgroundColor: brightness == Brightness.light
        ? const Color(0xfff2f3f7)
        : scheme.surface,
    appBarTheme: AppBarTheme(
      backgroundColor: brightness == Brightness.light
          ? const Color(0xff3d6db4)
          : const Color(0xff2f4d70),
      foregroundColor: Colors.white,
      elevation: 0,
      centerTitle: true,
      // 蓝色标题栏压矮一点，避免顶部一整块蓝色过于抢眼
      toolbarHeight: 46,
    ),
    cardTheme: const CardThemeData(
      elevation: 0,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.all(Radius.circular(8)),
      ),
    ),
    inputDecorationTheme: const InputDecorationTheme(
      border: OutlineInputBorder(
        borderRadius: BorderRadius.all(Radius.circular(8)),
      ),
    ),
  );
}

bool isDarkMode(BuildContext context) =>
    Theme.of(context).brightness == Brightness.dark;

Color primaryTextColor(BuildContext context) => isDarkMode(context)
    ? Theme.of(context).colorScheme.onSurface
    : const Color(0xff26324a);

Color secondaryTextColor(BuildContext context) => isDarkMode(context)
    ? Theme.of(context).colorScheme.onSurfaceVariant
    : const Color(0xff6b7280);

Color softCardColor(BuildContext context) => isDarkMode(context)
    ? Theme.of(context).colorScheme.surfaceContainer
    : Colors.white;

Color chipBackgroundColor(BuildContext context) => isDarkMode(context)
    ? Theme.of(context).colorScheme.surfaceContainerHighest
    : const Color(0xfff2f5fa);

Color subtleBorderColor(BuildContext context) => isDarkMode(context)
    ? Theme.of(context).colorScheme.outlineVariant
    : const Color(0xffdbe4f0);

Color activeRowColor(BuildContext context) => isDarkMode(context)
    ? Theme.of(context).colorScheme.primaryContainer.withValues(alpha: 0.35)
    : const Color(0xffeef5ff);

class NativeBridge {
  static const _native = MethodChannel('safelink/native');
  static const _core = MethodChannel('safelink/core');
  static const _events = EventChannel('safelink/events');

  Stream<Map<String, dynamic>> get events {
    return _events.receiveBroadcastStream().map((event) {
      return Map<String, dynamic>.from(event as Map);
    });
  }

  Future<Map<String, dynamic>> call(
    String method, [
    Map<String, Object?> args = const {},
  ]) async {
    try {
      final result = await _native.invokeMethod<Object?>(method, args);
      if (result is Map) {
        return Map<String, dynamic>.from(result);
      }
      return {'value': result};
    } on MissingPluginException {
      return {'missing_plugin': true};
    }
  }

  Future<String?> secureRead(String key) async {
    try {
      return await _native.invokeMethod<String>('secureRead', {'key': key});
    } on MissingPluginException {
      return null;
    }
  }

  Future<void> secureWrite(String key, String value) async {
    try {
      await _native.invokeMethod<bool>('secureWrite', {
        'key': key,
        'value': value,
      });
    } on MissingPluginException {
      return;
    }
  }

  Future<String> coreVersion() async {
    try {
      final version = await _core.invokeMethod<String>('version');
      return (version == null || version.isEmpty) ? '未知' : version;
    } on MissingPluginException {
      return '未知';
    }
  }

  Future<Map<String, dynamic>?> coreCall(
    String method, [
    Map<String, Object?> payload = const {},
  ]) async {
    try {
      final raw = await _core.invokeMethod<String>('call', {
        'method': method,
        'payload': jsonEncode(payload),
      });
      if (raw == null || raw.isEmpty) {
        return null;
      }
      return Map<String, dynamic>.from(jsonDecode(raw) as Map);
    } on MissingPluginException {
      return null;
    }
  }
}

class AppController extends ChangeNotifier {
  AppController(this.native) {
    _eventSub = native.events.listen(_handleNativeEvent, onError: (_) {});
    _runtimePoll = Timer.periodic(const Duration(seconds: 2), (_) {
      _pollRuntimeStatus();
    });
    _loadAboutInfo();
  }

  static const _proxySocksAddr = '127.0.0.1:10808';
  static const _proxyHttpAddr = '127.0.0.1:10809';
  static const _proxyClashAddr = '127.0.0.1:10810';
  static const _storeKey = 'safelink_state_v1';
  static const _settingsVersion = 2;
  // 跟 Go 侧 subscriptionclient 用的是同一个值，机场按 UA 决定给不给用量头，两端得一致
  static const _subscriptionUserAgent = 'ClashVerge/2.4.1';
  final NativeBridge native;
  StreamSubscription<Map<String, dynamic>>? _eventSub;
  Timer? _runtimePoll;
  bool _pollingRuntime = false;
  String _lastTrafficError = '';

  bool loaded = false;
  bool busy = false;
  bool allowInsecureSubscriptionCerts = true;
  int selectedIndex = 0;
  String nodeFilter = 'all';
  String nodeSort = 'default';
  String themeMode = 'system';
  String proxyMode = 'rule';
  String selectedNodeId = '';
  RuntimeSession? activeSession;
  int uploadSpeedBps = 0;
  int downloadSpeedBps = 0;
  int uploadTotalBytes = 0;
  int downloadTotalBytes = 0;
  final subscriptions = <SubscriptionSource>[];
  final proxyNodes = <ProxyNode>[];
  final tunnels = <TunnelConfig>[];
  final routeRules = <ProxyRule>[];
  final logs = <LogEntry>[];
  final collapsedSubscriptions = <String>{};
  String coreVersionText = '获取中…';
  String deviceInfoText = '获取中…';

  String get proxyModeLabel => switch (proxyMode) {
    'global' => '全局模式',
    'direct' => '直连模式',
    _ => '规则模式',
  };

  Future<void> load() async {
    final raw = await native.secureRead(_storeKey);
    if (raw != null && raw.isNotEmpty) {
      try {
        final data = jsonDecode(raw) as Map<String, dynamic>;
        proxyMode = data['proxy_mode'] as String? ?? 'rule';
        nodeFilter = data['node_filter'] as String? ?? 'all';
        nodeSort = switch (data['node_sort'] as String? ?? 'default') {
          'latency' => 'latency',
          'speed' => 'speed',
          _ => 'default',
        };
        themeMode = switch (data['theme_mode'] as String? ?? 'system') {
          'light' => 'light',
          'dark' => 'dark',
          _ => 'system',
        };
        final settingsVersion = data['settings_version'] as int? ?? 0;
        allowInsecureSubscriptionCerts = settingsVersion < _settingsVersion
            ? true
            : data['allow_insecure_subscription_certs'] as bool? ?? true;
        selectedNodeId = data['selected_node_id'] as String? ?? '';
        collapsedSubscriptions
          ..clear()
          ..addAll(
            (data['collapsed_subscriptions'] as List? ?? const [])
                .whereType<String>(),
          );
        subscriptions
          ..clear()
          ..addAll(
            _list(data['subscriptions']).map(SubscriptionSource.fromJson),
          );
        proxyNodes
          ..clear()
          ..addAll(_list(data['proxy_nodes']).map(ProxyNode.fromJson));
        tunnels
          ..clear()
          ..addAll(_list(data['tunnels']).map(TunnelConfig.fromJson));
        routeRules
          ..clear()
          ..addAll(
            _normalizeRouteRules(
              _list(
                data['rule_mode_rules'] ?? data['route_rules'],
              ).map(ProxyRule.fromJson).toList(),
              _int(
                data['rule_mode_rules_version'] ?? data['route_rules_version'],
              ),
            ),
          );
        logs
          ..clear()
          // 旧版本把内核日志也写进了本地存储，升级后仍会显示，读取时一并滤掉。
          ..addAll(
            _list(data['logs'])
                .map(LogEntry.fromJson)
                .where((item) => !item.message.startsWith('sing-box:')),
          );
      } catch (error) {
        _log('error', '读取本地数据失败: $error');
      }
    }
    if (routeRules.isEmpty) {
      routeRules.addAll(ProxyRule.defaultRules());
    }
    loaded = true;
    notifyListeners();
    await _restoreActiveSession();
  }

  Future<void> save() async {
    final data = {
      'settings_version': _settingsVersion,
      'proxy_mode': proxyMode,
      'node_filter': nodeFilter,
      'node_sort': nodeSort,
      'theme_mode': themeMode,
      'selected_node_id': selectedNodeId,
      'collapsed_subscriptions': collapsedSubscriptions.toList(),
      'allow_insecure_subscription_certs': allowInsecureSubscriptionCerts,
      'subscriptions': subscriptions.map((item) => item.toJson()).toList(),
      'proxy_nodes': proxyNodes.map((item) => item.toJson()).toList(),
      'tunnels': tunnels.map((item) => item.toJson()).toList(),
      'rule_mode_rules': routeRules.map((item) => item.toJson()).toList(),
      'rule_mode_rules_version': ProxyRule.currentVersion,
      'logs': logs.take(200).map((item) => item.toJson()).toList(),
    };
    await native.secureWrite(_storeKey, jsonEncode(data));
  }

  void selectPage(int index) {
    selectedIndex = index;
    notifyListeners();
  }

  void setNodeFilter(String value) {
    nodeFilter = value;
    notifyListeners();
  }

  Future<void> setNodeSort(String value) async {
    nodeSort = value;
    await save();
    notifyListeners();
  }

  Future<void> setThemeMode(String value) async {
    themeMode = value;
    _log('info', '外观切换为 ${themeModeLabel(value)}');
    // 先刷新 UI 再持久化，避免主题切换被存储调用阻塞
    notifyListeners();
    await save();
  }

  static String themeModeLabel(String value) => switch (value) {
    'light' => '浅色',
    'dark' => '深色',
    _ => '跟随系统',
  };

  List<ProxyNode> get visibleProxyNodes {
    final nodes = switch (nodeFilter) {
      'favorite' => proxyNodes.where((item) => item.favorite).toList(),
      _ => List<ProxyNode>.from(proxyNodes),
    };
    final order = nodeFilter == 'auto' ? 'latency' : nodeSort;
    switch (order) {
      case 'latency':
        nodes.sort((a, b) => a.latencySortValue.compareTo(b.latencySortValue));
      case 'speed':
        nodes.sort(
          (a, b) => (b.speedMbps ?? -1).compareTo(a.speedMbps ?? -1),
        );
    }
    return nodes;
  }

  ProxyNode? get currentNode {
    final name = activeSession?.name;
    if (name == null || name.isEmpty) return null;
    for (final node in proxyNodes) {
      if (node.name == name) return node;
    }
    return null;
  }

  ProxyNode? get selectedNode {
    for (final node in proxyNodes) {
      if (node.id == selectedNodeId) return node;
    }
    return proxyNodes.firstOrNull;
  }

  List<ProxyNode> nodesOfSubscription(SubscriptionSource source) =>
      proxyNodes.where((item) => item.subscriptionId == source.id).toList();

  Future<void> selectNode(BuildContext context, ProxyNode node) async {
    selectedNodeId = node.id;
    notifyListeners();
    await save();
    final session = activeSession;
    if (session == null || session.name == node.name) return;
    // 连接开着时切换节点要把运行中的会话一起切过去，
    // 否则 activeSession 还指着旧节点，行高亮和状态卡都留在原处
    if (session.kind != 'proxy-vpn' && session.kind != 'local-proxy') return;
    // ignore: use_build_context_synchronously
    await startProxyNode(context, node, vpnMode: session.kind == 'proxy-vpn');
  }

  void toggleSubscriptionCollapsed(String id) {
    if (!collapsedSubscriptions.add(id)) {
      collapsedSubscriptions.remove(id);
    }
    notifyListeners();
  }

  Future<void> toggleConnection(BuildContext context) async {
    if (activeSession != null) {
      await stopRuntime();
      return;
    }
    final node = selectedNode;
    if (node == null) {
      _log('warn', '请先添加订阅并选择节点');
      notifyListeners();
      return;
    }
    await startProxyNode(context, node, vpnMode: true);
  }

  Future<void> importSubscription(String url) async {
    await _run('导入订阅', () async {
      final trimmedUrl = url.trim();
      if (trimmedUrl.isEmpty) {
        throw const FormatException('请填写订阅 URL');
      }
      final source = SubscriptionSource(
        id: _id(),
        // 先用域名占位，下载成功后由订阅内容里的名称覆盖
        name: Uri.tryParse(trimmedUrl)?.host ?? '新订阅',
        url: trimmedUrl,
        enabled: true,
        lastRefresh: DateTime.now(),
      );
      subscriptions.add(source);
      await refreshSubscription(source);
    });
  }

  Future<void> importScannedText(String text) async {
    final content = text.trim();
    if (content.isEmpty) {
      _log('warn', '二维码内容为空，无法导入');
      notifyListeners();
      return;
    }
    final uri = Uri.tryParse(content);
    if (uri != null && (uri.isScheme('http') || uri.isScheme('https'))) {
      await importSubscription(content);
      return;
    }
    await _run('扫码导入', () async {
      // 兼容整体 base64 编码的订阅内容
      var body = content;
      if (!content.contains('://') &&
          !content.startsWith('{') &&
          !content.startsWith('[')) {
        try {
          body = utf8.decode(base64Decode(content));
        } catch (_) {}
      }
      final source = SubscriptionSource(
        id: _id(),
        name: '扫码导入',
        url: '',
        enabled: false,
        lastRefresh: DateTime.now(),
      );
      final parsed = await native.coreCall('parseSubscription', {'body': body});
      final imported = parsed != null && parsed['ok'] == true
          ? _importCoreSubscription(source, parsed['data'])
          : _parseSubscription(source, body);
      if (imported <= 0) {
        throw const FormatException('二维码内容不是有效的订阅或节点');
      }
      subscriptions.add(source);
      _log('info', '扫码导入 $imported 项');
      await save();
    });
  }

  Future<void> _restoreActiveSession() async {
    // 上次运行时内核可能已经崩了，而 VPN 服务还持着 tun，全机流量被引向死隧道。
    // 恢复会话状态之前先跟原生对一次账，把残留的隧道拆掉。
    try {
      final result = await native.call('reconcileRuntime');
      if (result['stale_tunnel_cleared'] == true) {
        _log('warn', '检测到内核已退出但隧道仍在，已清理');
      }
    } catch (error) {
      _log('warn', '运行状态对账失败: $error');
    }
    try {
      final vpnResp = await native.coreCall('vpnStatus');
      if (vpnResp != null && vpnResp['ok'] == true) {
        final data = Map<String, dynamic>.from(vpnResp['data'] as Map? ?? {});
        final state = data['state'] as String? ?? '';
        if (state == 'running' ||
            state == 'connecting' ||
            state == 'reconnecting') {
          activeSession = RuntimeSession(
            kind: data['kind'] as String? ?? 'safelink-vpn',
            name: data['name'] as String? ?? 'SafeLink VPN',
            startedAt:
                DateTime.tryParse(data['started_at'] as String? ?? '') ??
                DateTime.now(),
          );
          _log('info', '检测到正在运行的 ${activeSession!.kindLabel}，已同步状态');
          notifyListeners();
          return;
        }
      }
      final proxy = await native.call('proxyStatus');
      if (proxy['state'] == 'running') {
        final startedMs = _int(proxy['started_at']);
        activeSession = RuntimeSession(
          kind: 'local-proxy',
          name: proxy['node'] as String? ?? '本地代理',
          startedAt: startedMs > 0
              ? DateTime.fromMillisecondsSinceEpoch(startedMs)
              : DateTime.now(),
        );
        _log('info', '检测到正在运行的本地代理，已同步状态');
        notifyListeners();
      }
    } catch (error) {
      _log('warn', '同步运行状态失败: $error');
    }
  }

  Future<void> refreshSubscription(SubscriptionSource source) async {
    await _run(
      '刷新订阅',
      () async {
        final fetched = await _fetchSubscription(source.url);
        // 名称以订阅内容为准，需在解析节点前确定（节点名带订阅前缀）
        if (fetched.title.isNotEmpty) {
          source.name = fetched.title;
        } else if (source.name.trim().isEmpty) {
          source.name = Uri.tryParse(source.url)?.host ?? '新订阅';
        }
        final parsed = await native.coreCall('parseSubscription', {
          'body': fetched.body,
        });
        final imported = parsed != null && parsed['ok'] == true
            ? _importCoreSubscription(source, parsed['data'])
            : _parseSubscription(source, fetched.body);
        source.lastRefresh = DateTime.now();
        source.lastError = '';
        _log('info', '订阅 ${source.name} 已更新，导入 $imported 项');
        await save();
      },
      onError: (error) {
        source.lastRefresh = DateTime.now();
        source.lastError = error.toString();
      },
    );
  }

  Future<void> refreshAllSubscriptions() async {
    for (final source in List<SubscriptionSource>.from(subscriptions)) {
      if (source.enabled) {
        await refreshSubscription(source);
      }
    }
  }

  Future<void> deleteSubscription(SubscriptionSource source) async {
    subscriptions.removeWhere((item) => item.id == source.id);
    proxyNodes.removeWhere((item) => item.subscriptionId == source.id);
    tunnels.removeWhere((item) => item.subscriptionId == source.id);
    _log('info', '已删除订阅 ${source.name}');
    await save();
    notifyListeners();
  }

  Future<void> startProxyNode(
    BuildContext context,
    ProxyNode node, {
    required bool vpnMode,
  }) async {
    await _run('连接节点', () async {
      if (vpnMode) {
        final canSwitch = await _confirmSessionSwitch(context, 'proxy-vpn');
        if (!canSwitch) return;
      }
      final configJson = await _buildProxyConfig(node);
      if (vpnMode) {
        final prepared = await native.call('prepareVpn');
        if (prepared['prepared'] == false) {
          _log('warn', '等待 Android VPN 授权');
          return;
        }
        await native.call('startVpnSession', {
          'kind': 'proxy-vpn',
          'name': node.name,
          'routes': ['0.0.0.0/0'],
          'dns': ['1.1.1.1', '8.8.8.8'],
          'configJson': configJson,
          'socksAddr': _proxySocksAddr,
          'clashApiAddr': _proxyClashAddr,
        });
        activeSession = RuntimeSession(
          kind: 'proxy-vpn',
          name: node.name,
          startedAt: DateTime.now(),
        );
      } else {
        await native.call('startLocalProxy', {
          'node': node.name,
          'mode': proxyMode,
          'configJson': configJson,
          'socksAddr': _proxySocksAddr,
          'clashApiAddr': _proxyClashAddr,
        });
        activeSession = RuntimeSession(
          kind: 'local-proxy',
          name: node.name,
          startedAt: DateTime.now(),
        );
      }
      _log('info', '已启动 ${node.name}');
      _refreshRuleSets();
      await save();
      notifyListeners();
    });
  }

  // 规则集内置在包里，启动不依赖网络。这里只是趁着代理已经通了把过期的那份换掉，
  // 桌面端在内核起来之后自己会做，Android 的内核由 Kotlin 启动、不走那条路径，得显式调。
  // 失败无所谓，内置的一直可用，所以不 await、不报错给用户。
  void _refreshRuleSets() {
    native.coreCall('refreshRuleSets').catchError((Object error) {
      _log('debug', '规则集更新未启动: $error');
      return null;
    });
  }

  Future<String> _buildProxyConfig(ProxyNode node) async {
    final response = await native.coreCall('buildProxyConfig', {
      'mode': proxyMode,
      'node': node.toCoreJson(),
      'rules': routeRules.map((item) => item.toJson()).toList(),
      'socks_addr': _proxySocksAddr,
      'http_addr': _proxyHttpAddr,
      'clash_api_addr': _proxyClashAddr,
    });
    if (response == null) {
      throw StateError('mobile core unavailable');
    }
    if (response['ok'] != true) {
      throw StateError(
        response['message'] as String? ?? 'build proxy config failed',
      );
    }
    final data = Map<String, dynamic>.from(response['data'] as Map? ?? {});
    final configJson = data['config'] as String?;
    if (configJson == null || configJson.isEmpty) {
      throw StateError('empty sing-box config');
    }
    _log('info', '已生成 ${node.name} 的 sing-box 配置');
    return configJson;
  }

  Future<void> stopRuntime() async {
    await _run('停止连接', () async {
      final kind = activeSession?.kind;
      if (kind == 'proxy-vpn' || kind == 'safelink-vpn') {
        await native.call('stopVpnSession');
      }
      if (kind == 'local-proxy') {
        await native.call('stopLocalProxy');
      }
      activeSession = null;
      _log('info', '连接已停止');
      await save();
      notifyListeners();
    });
  }

  Future<void> speedTestProxyNode(ProxyNode node) async {
    await _run(
      '速度测试',
      () async {
        final result = await _coreTestResult('speedTestProxyNode', node);
        _applyTestResult(node, result);
        final latency = node.latencyMs == null ? '' : '，延迟 ${node.latencyMs} ms';
        _log('info', '${node.name} 速度 ${formatMbps(node.speedMbps)}$latency');
        await save();
        notifyListeners();
      },
      onError: (error) {
        node.speedMbps = null;
        node.lastError = error.toString();
      },
    );
  }

  Future<void> testAllProxyNodes() async {
    await _run('批量延迟测试', () async {
      final response = await native.coreCall('testProxyNodes', {
        'mode': proxyMode,
        'nodes': proxyNodes.map((item) => item.toCoreJson()).toList(),
        'rules': routeRules.map((item) => item.toJson()).toList(),
        'timeout_ms': 5000,
      });
      if (response == null || response['ok'] != true) {
        throw StateError(response?['message'] as String? ?? 'test failed');
      }
      final data = Map<String, dynamic>.from(response['data'] as Map? ?? {});
      final results = (data['results'] as List? ?? const []).whereType<Map>();
      var succeeded = 0;
      var failed = 0;
      String firstError = '';
      for (final raw in results) {
        final result = Map<String, dynamic>.from(raw);
        final name =
            (result['node_name'] as String?) ??
            (result['NodeName'] as String?) ??
            '';
        final node = proxyNodes.where((item) => item.name == name).firstOrNull;
        if (node != null) {
          _applyTestResult(node, result);
          if (node.lastError.isEmpty) {
            succeeded++;
          } else {
            failed++;
            if (firstError.isEmpty) {
              firstError = node.lastError;
            }
          }
        }
      }
      if (failed > 0 && succeeded == 0) {
        _log('error', '节点延迟测试全部失败: $firstError');
      } else if (failed > 0) {
        _log('warn', '节点延迟测试完成，成功 $succeeded 个，失败 $failed 个');
      } else {
        _log('info', '节点延迟测试完成，成功 $succeeded 个');
      }
      await save();
      notifyListeners();
    });
  }

  Future<void> autoSelectNode(BuildContext context) async {
    if (proxyNodes.isEmpty) {
      _log('warn', '暂无可选节点');
      notifyListeners();
      return;
    }
    await testAllProxyNodes();
    final candidates =
        proxyNodes.where((item) => item.latencyMs != null).toList()
          ..sort((a, b) => a.latencySortValue.compareTo(b.latencySortValue));
    final selected = candidates.isNotEmpty
        ? candidates.first
        : proxyNodes.first;
    // ignore: use_build_context_synchronously
    await startProxyNode(context, selected, vpnMode: false);
  }

  Future<Map<String, dynamic>> _coreTestResult(
    String method,
    ProxyNode node,
  ) async {
    final response = await native.coreCall(method, {
      'mode': proxyMode,
      'node': node.toCoreJson(),
      'rules': routeRules.map((item) => item.toJson()).toList(),
      'timeout_ms': method == 'speedTestProxyNode' ? 15000 : 8000,
    });
    if (response == null || response['ok'] != true) {
      throw StateError(response?['message'] as String? ?? '$method failed');
    }
    final data = Map<String, dynamic>.from(response['data'] as Map? ?? {});
    return Map<String, dynamic>.from(data['result'] as Map? ?? {});
  }

  void _applyTestResult(ProxyNode node, Map<String, dynamic> result) {
    final ok = result['ok'] as bool? ?? result['OK'] as bool? ?? false;
    final error =
        result['error'] as String? ?? result['Error'] as String? ?? '';
    node.lastTestedAt = DateTime.now();
    if (!ok) {
      node.lastError = error.isEmpty ? '测试失败' : error;
      return;
    }
    node.lastError = '';
    final latency = _int(result['latency_ms'] ?? result['LatencyMS']);
    if (latency > 0) {
      node.latencyMs = latency;
    }
    final speed = _double(result['speed_mbps'] ?? result['SpeedMbps']);
    if (speed > 0) {
      node.speedMbps = speed;
    }
  }

  Future<void> setProxyMode(String mode) async {
    proxyMode = mode;
    _log('info', '代理模式切换为 $mode');
    await save();
    notifyListeners();
  }

  Future<void> setAllowInsecureSubscriptionCerts(bool value) async {
    allowInsecureSubscriptionCerts = value;
    _log('info', value ? '已允许订阅使用不受信任证书' : '已恢复订阅证书校验');
    await save();
    notifyListeners();
  }

  Future<void> startVpnTunnel(BuildContext context, TunnelConfig tunnel) async {
    await _run('启动 VPN', () async {
      final canSwitch = await _confirmSessionSwitch(context, 'safelink-vpn');
      if (!canSwitch) return;
      final prepared = await native.call('prepareVpn');
      if (prepared['prepared'] == false) {
        _log('warn', '等待 Android VPN 授权');
        return;
      }
      await native.call('startVpnSession', {
        'kind': 'safelink-vpn',
        'name': tunnel.name,
        'routes': tunnel.autoRoute ? ['0.0.0.0/0'] : [tunnel.tunSubnet],
        'dns': tunnel.dns.isEmpty ? ['1.1.1.1'] : tunnel.dns,
        'tunnelJson': jsonEncode(tunnel.toCoreJson()),
      });
      tunnel.running = true;
      activeSession = RuntimeSession(
        kind: 'safelink-vpn',
        name: tunnel.name,
        startedAt: DateTime.now(),
      );
      _log('info', 'SafeLink VPN ${tunnel.name} 已启动');
      await save();
      notifyListeners();
    });
  }

  Future<void> _pollRuntimeStatus() async {
    final session = activeSession;
    if (session == null || _pollingRuntime) return;
    _pollingRuntime = true;
    try {
      Map<String, dynamic>? status;
      if (session.kind == 'local-proxy') {
        status = await native.call('proxyStatus');
      } else if (session.kind == 'proxy-vpn' ||
          session.kind == 'safelink-vpn') {
        final response = await native.coreCall('vpnStatus');
        if (response?['ok'] == true) {
          status = Map<String, dynamic>.from(response?['data'] as Map? ?? {});
        }
        if (session.kind == 'proxy-vpn' && status != null) {
          // tun2socks 不统计流量，叠加 sing-box clash API 的数据；
          // 若 sing-box 进程已退出（事件可能丢失），视为会话结束
          final proxy = await native.call('proxyStatus');
          if (proxy['missing_plugin'] != true) {
            final proxyState = proxy['state'] as String? ?? '';
            if (proxyState == 'running') {
              status['upload_total_bytes'] = proxy['upload_total_bytes'];
              status['download_total_bytes'] = proxy['download_total_bytes'];
              status['upload_speed_bps'] = proxy['upload_speed_bps'];
              status['download_speed_bps'] = proxy['download_speed_bps'];
              status['traffic_error'] = proxy['traffic_error'];
            } else {
              status['state'] = 'stopped';
              final proxyError = proxy['last_error'] as String? ?? '';
              if (proxyError.isNotEmpty) {
                status['last_error'] = proxyError;
              }
            }
          }
        }
      }
      final state = status?['state'] as String? ?? '';
      uploadTotalBytes = _int(
        status?['upload_total_bytes'] ?? status?['bytes_out'],
      );
      downloadTotalBytes = _int(
        status?['download_total_bytes'] ?? status?['bytes_in'],
      );
      uploadSpeedBps = _int(status?['upload_speed_bps']);
      downloadSpeedBps = _int(status?['download_speed_bps']);
      _reportTrafficError(status?['traffic_error'] as String? ?? '');
      if (state == 'stopped' || state == 'error') {
        // 启动窗口内容忍 stopped：服务尚未完成初始化时不误判为断开
        final starting =
            DateTime.now().difference(session.startedAt) <
            const Duration(seconds: 15);
        if (starting && state == 'stopped') {
          notifyListeners();
          return;
        }
        activeSession = null;
        uploadSpeedBps = 0;
        downloadSpeedBps = 0;
        final error =
            status?['last_error'] as String? ??
            status?['message'] as String? ??
            '';
        if (state == 'error' && error.isNotEmpty) {
          _log('error', '${session.name}: $error');
        } else {
          _log('info', '${session.name}: $state');
        }
      }
      notifyListeners();
    } finally {
      _pollingRuntime = false;
    }
  }

  // 流量取不到时界面只会一直显示 0，看不出是没跑流量还是统计接口坏了。
  // 原因变化时记一条日志，同一个原因不重复刷屏。
  void _reportTrafficError(String error) {
    if (error == _lastTrafficError) return;
    _lastTrafficError = error;
    if (error.isNotEmpty) {
      _log('warn', '流量统计接口异常: $error');
    }
  }

  Future<void> clearLogs() async {
    logs.clear();
    await save();
    notifyListeners();
  }

  Future<void> clearTestResults() async {
    for (final node in proxyNodes) {
      node.latencyMs = null;
      node.speedMbps = null;
      node.lastError = '';
      node.lastTestedAt = null;
    }
    _log('info', '已清除全部节点的测试结果');
    await save();
    notifyListeners();
  }

  Future<void> _loadAboutInfo() async {
    coreVersionText = await native.coreVersion();
    final info = await native.call('getDeviceInfo');
    if (info['missing_plugin'] == true || info['model'] == null) {
      deviceInfoText = '未知';
    } else {
      final release = info['release'] ?? '';
      final model = info['model'] ?? '';
      final arch = info['arch'] ?? '';
      deviceInfoText = 'Android $release · $model · $arch';
    }
    notifyListeners();
  }

  @override
  void dispose() {
    _runtimePoll?.cancel();
    _eventSub?.cancel();
    super.dispose();
  }

  Future<void> _run(
    String label,
    Future<void> Function() action, {
    void Function(Object error)? onError,
  }) async {
    busy = true;
    notifyListeners();
    try {
      await action();
    } catch (error) {
      onError?.call(error);
      _log('error', '$label 失败: $error');
      await save();
      notifyListeners();
    } finally {
      busy = false;
      notifyListeners();
    }
  }

  Future<bool> _confirmSessionSwitch(
    BuildContext context,
    String nextKind,
  ) async {
    final current = activeSession;
    if (current == null || current.kind == nextKind) {
      return true;
    }
    if (current.kind == 'local-proxy') {
      // 本地代理可直接切换，但需停掉旧的前台服务，避免通知栏残留
      await stopRuntime();
      return true;
    }
    final ok = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('切换连接'),
        content: Text('当前正在运行 ${current.name}，需要先停止再启动新连接。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('切换'),
          ),
        ],
      ),
    );
    if (ok == true) {
      await stopRuntime();
      return true;
    }
    return false;
  }

  Future<({String body, String title, SubscriptionUsage usage})>
  _fetchSubscription(String url) async {
    final uri = Uri.parse(url);
    final client = HttpClient();
    if (allowInsecureSubscriptionCerts) {
      client.badCertificateCallback = (cert, host, port) => true;
    }
    try {
      final request = await client
          .getUrl(uri)
          .timeout(const Duration(seconds: 20));
      // 部分机场只对已知客户端返回 Clash 约定的套餐用量头
      request.headers.set(HttpHeaders.userAgentHeader, _subscriptionUserAgent);
      final response = await request.close().timeout(
        const Duration(seconds: 20),
      );
      if (response.statusCode < 200 || response.statusCode >= 300) {
        throw HttpException('HTTP ${response.statusCode}', uri: uri);
      }
      final body = await response.transform(utf8.decoder).join();
      return (
        body: body,
        title: _subscriptionTitle(response.headers),
        usage: SubscriptionUsage.fromHeader(
          response.headers.value('subscription-userinfo'),
        ),
      );
    } on HandshakeException catch (error) {
      if (allowInsecureSubscriptionCerts) {
        rethrow;
      }
      throw StateError('订阅证书校验失败，可在设置中允许不受信任订阅证书后重试: $error');
    } finally {
      client.close(force: true);
    }
  }

  // 订阅名称优先取机场约定的 profile-title 头，其次取附件文件名
  static String _subscriptionTitle(HttpHeaders headers) {
    var title = headers.value('profile-title')?.trim() ?? '';
    if (title.startsWith('base64:')) {
      try {
        title = utf8.decode(base64Decode(title.substring(7).trim()));
      } catch (_) {
        title = '';
      }
    }
    if (title.isNotEmpty) {
      return title;
    }
    final disposition = headers.value('content-disposition') ?? '';
    final encoded = RegExp(
      "filename\\*=(?:utf-8|UTF-8)''([^;]+)",
    ).firstMatch(disposition);
    var filename = encoded != null
        ? Uri.decodeComponent(encoded.group(1)!.trim())
        : RegExp(
                'filename="?([^";]+)"?',
              ).firstMatch(disposition)?.group(1)?.trim() ??
              '';
    final dot = filename.lastIndexOf('.');
    if (dot > 0) {
      filename = filename.substring(0, dot);
    }
    return filename.trim();
  }

  int _parseSubscription(SubscriptionSource source, String body) {
    proxyNodes.removeWhere((item) => item.subscriptionId == source.id);
    tunnels.removeWhere((item) => item.subscriptionId == source.id);
    final trimmed = body.trim();
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
      final decoded = jsonDecode(trimmed);
      if (decoded is Map<String, dynamic>) {
        final tunnelItems = decoded['tunnels'];
        if (tunnelItems is List) {
          final imported = tunnelItems.map((item) {
            final map = Map<String, dynamic>.from(item as Map);
            return TunnelConfig.fromSubscription(source, map);
          }).toList();
          tunnels.addAll(imported);
          source.kind = 'vpn';
          return imported.length;
        }
        final outbounds = decoded['outbounds'] ?? decoded['proxies'];
        if (outbounds is List) {
          final imported = outbounds
              .whereType<Map>()
              .map((item) {
                return ProxyNode.fromMap(
                  source,
                  Map<String, dynamic>.from(item),
                );
              })
              .where((item) => item.server.isNotEmpty && item.port > 0)
              .toList();
          proxyNodes.addAll(imported);
          source.kind = 'proxy';
          return imported.length;
        }
      }
    }
    final imported = <ProxyNode>[];
    for (final line in const LineSplitter().convert(body)) {
      final node = ProxyNode.fromUri(source, line.trim());
      if (node != null) {
        imported.add(node);
      }
    }
    if (imported.isEmpty) {
      throw FormatException('unsupported subscription format');
    }
    proxyNodes.addAll(imported);
    source.kind = 'proxy';
    return imported.length;
  }

  int _importCoreSubscription(SubscriptionSource source, Object? rawData) {
    final data = Map<String, dynamic>.from(rawData as Map? ?? {});
    proxyNodes.removeWhere((item) => item.subscriptionId == source.id);
    tunnels.removeWhere((item) => item.subscriptionId == source.id);
    final kind = data['kind'] as String? ?? 'proxy';
    if (kind == 'vpn') {
      final imported = (data['tunnels'] as List? ?? const [])
          .whereType<Map>()
          .map(
            (item) => TunnelConfig.fromSubscription(
              source,
              Map<String, dynamic>.from(item),
            ),
          )
          .toList();
      tunnels.addAll(imported);
      source.kind = 'vpn';
      return imported.length;
    }
    final imported = (data['nodes'] as List? ?? const [])
        .whereType<Map>()
        .map(
          (item) => ProxyNode.fromMap(source, Map<String, dynamic>.from(item)),
        )
        .where((item) => item.server.isNotEmpty && item.port > 0)
        .toList();
    proxyNodes.addAll(imported);
    source.kind = 'proxy';
    return imported.length;
  }

  void _handleNativeEvent(Map<String, dynamic> event) {
    final type = event['type'] as String? ?? '';
    final payload = Map<String, dynamic>.from(event['payload'] as Map? ?? {});
    if (type == 'proxy.log') {
      // sing-box 内核日志只留在原生层做失败诊断，不进连接日志。
      return;
    }
    if (type == 'vpn.status' || type == 'proxy.status') {
      final state = payload['state'] as String? ?? '';
      if (state == 'running' || state == 'starting') {
        _syncSessionFromStatus(type, payload);
      } else if (state == 'error') {
        final kind = activeSession?.kind;
        activeSession = null;
        _releaseStaleTunnel(kind);
      } else if (state == 'stopped') {
        // stopped 事件无法归因到具体会话，新会话启动窗口内忽略，
        // 避免停止旧服务时延迟到达的事件误清新会话
        final current = activeSession;
        final starting =
            current != null &&
            DateTime.now().difference(current.startedAt) <
                const Duration(seconds: 15);
        if (!starting) {
          final kind = current?.kind;
          activeSession = null;
          _releaseStaleTunnel(kind);
        }
      }
      final message =
          payload['message'] as String? ??
          payload['last_error'] as String? ??
          '';
      _log(
        state == 'error' ? 'error' : 'info',
        message.isEmpty ? '$type: $state' : '$type: $state $message',
      );
      notifyListeners();
    }
  }

  // 内核挂掉时界面会变成“未连接”，但 tun 还在把全机流量往死隧道里灌，
  // 所以清掉会话的同时必须把原生的 VPN 服务也停掉。
  void _releaseStaleTunnel(String? kind) {
    if (kind != 'proxy-vpn' && kind != 'safelink-vpn') {
      return;
    }
    native.call('stopVpnSession').catchError((Object error) {
      _log('warn', '清理残留隧道失败: $error');
      return <String, dynamic>{};
    });
  }

  void _syncSessionFromStatus(String type, Map<String, dynamic> payload) {
    var kind = payload['kind'] as String? ?? '';
    if (kind.isEmpty && type == 'proxy.status') {
      // sing-box 状态事件用 mode 区分本地代理与全局 VPN 代理
      kind = (payload['mode'] as String?) == 'vpn' ? 'proxy-vpn' : 'local-proxy';
    }
    if (kind.isEmpty) return;
    final name =
        payload['session'] as String? ??
        payload['node'] as String? ??
        activeSession?.name ??
        '';
    if (name.isEmpty) return;
    final current = activeSession;
    if (current != null && current.kind == kind && current.name == name) {
      return;
    }
    activeSession = RuntimeSession(
      kind: kind,
      name: name,
      startedAt: DateTime.now(),
    );
  }

  void _log(String level, String message) {
    logs.insert(
      0,
      LogEntry(level: level, message: message, at: DateTime.now()),
    );
    if (logs.length > 300) {
      logs.removeRange(300, logs.length);
    }
  }

  static List<Map<String, dynamic>> _list(Object? value) {
    if (value is! List) return const [];
    return value
        .whereType<Map>()
        .map((item) => Map<String, dynamic>.from(item))
        .toList();
  }

  static List<ProxyRule> _normalizeRouteRules(
    List<ProxyRule> rules,
    int version,
  ) {
    if (rules.isEmpty) {
      return ProxyRule.defaultRules();
    }
    final normalized = rules.map((item) => item.normalized()).toList();
    if (version < ProxyRule.currentVersion &&
        !normalized.any((item) => item.type == ProxyRule.typeRuleSet)) {
      normalized.addAll(
        ProxyRule.defaultRules().where(
          (item) => item.type == ProxyRule.typeRuleSet,
        ),
      );
    }
    return normalized;
  }
}

class SafeLinkShell extends StatelessWidget {
  const SafeLinkShell({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: controller,
      builder: (context, _) {
        final pages = [
          HomePage(controller: controller),
          DataPage(controller: controller),
          SettingsPage(controller: controller),
        ];
        return Scaffold(
          appBar: AppBar(
            leading: IconButton(
              tooltip: '扫码导入',
              onPressed: controller.busy
                  ? null
                  : () async {
                      final text = await Navigator.push<String>(
                        context,
                        MaterialPageRoute(
                          builder: (_) => const ScanImportPage(),
                        ),
                      );
                      if (text != null && text.isNotEmpty) {
                        await controller.importScannedText(text);
                      }
                    },
              icon: const Icon(Icons.qr_code_scanner),
            ),
            title: const Text(
              'SafeLink',
              style: TextStyle(fontWeight: FontWeight.w800),
            ),
            actions: [
              if (controller.busy)
                const Padding(
                  padding: EdgeInsets.only(right: 6),
                  child: SizedBox.square(
                    dimension: 18,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  ),
                ),
              IconButton(
                tooltip: '添加订阅',
                onPressed: controller.busy
                    ? null
                    : () => showSubscriptionDialog(context, controller),
                icon: const Icon(Icons.add),
              ),
            ],
          ),
          body: SafeArea(child: pages[controller.selectedIndex]),
          bottomNavigationBar: PillNavBar(
            selectedIndex: controller.selectedIndex,
            onSelected: controller.selectPage,
            items: const [
              (Icons.home_outlined, Icons.home, '首页'),
              (Icons.insert_chart_outlined, Icons.insert_chart, '数据'),
              (Icons.settings_outlined, Icons.settings, '设置'),
            ],
          ),
        );
      },
    );
  }
}

class PillNavBar extends StatelessWidget {
  const PillNavBar({
    super.key,
    required this.selectedIndex,
    required this.onSelected,
    required this.items,
  });

  final int selectedIndex;
  final ValueChanged<int> onSelected;
  final List<(IconData, IconData, String)> items;

  @override
  Widget build(BuildContext context) {
    // 仿 Shadowrocket 的悬浮胶囊导航条：整条浮起，选中项带灰色胶囊底
    final pillColor = isDarkMode(context)
        ? Theme.of(context).colorScheme.secondaryContainer
        : const Color(0xffe4e6ea);
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 0, 14, 10),
        child: Container(
          height: 64,
          decoration: BoxDecoration(
            color: softCardColor(context),
            borderRadius: BorderRadius.circular(32),
            boxShadow: [
              BoxShadow(
                color: const Color(
                  0xff23406f,
                ).withValues(alpha: isDarkMode(context) ? 0.3 : 0.12),
                blurRadius: 24,
                offset: const Offset(0, 8),
              ),
            ],
          ),
          child: Row(
            children: [
              for (var i = 0; i < items.length; i++)
                Expanded(
                  child: InkWell(
                    borderRadius: BorderRadius.circular(32),
                    onTap: () => onSelected(i),
                    child: Center(
                      child: AnimatedContainer(
                        duration: const Duration(milliseconds: 180),
                        curve: Curves.easeOut,
                        padding: const EdgeInsets.symmetric(
                          horizontal: 20,
                          vertical: 5,
                        ),
                        decoration: BoxDecoration(
                          color: selectedIndex == i
                              ? pillColor
                              : Colors.transparent,
                          borderRadius: BorderRadius.circular(24),
                        ),
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(
                              selectedIndex == i ? items[i].$2 : items[i].$1,
                              size: 22,
                              color: selectedIndex == i
                                  ? primaryTextColor(context)
                                  : secondaryTextColor(context),
                            ),
                            const SizedBox(height: 2),
                            Text(
                              items[i].$3,
                              style: TextStyle(
                                fontSize: 11,
                                fontWeight: selectedIndex == i
                                    ? FontWeight.w700
                                    : FontWeight.w500,
                                color: selectedIndex == i
                                    ? primaryTextColor(context)
                                    : secondaryTextColor(context),
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class ScanImportPage extends StatefulWidget {
  const ScanImportPage({super.key});

  @override
  State<ScanImportPage> createState() => _ScanImportPageState();
}

class _ScanImportPageState extends State<ScanImportPage> {
  final _scanner = MobileScannerController();
  bool _handled = false;

  @override
  void dispose() {
    _scanner.dispose();
    super.dispose();
  }

  void _onDetect(BarcodeCapture capture) {
    if (_handled) return;
    final value = capture.barcodes
        .map((item) => item.rawValue?.trim() ?? '')
        .firstWhere((item) => item.isNotEmpty, orElse: () => '');
    if (value.isEmpty) return;
    // 只取首个有效结果，避免连续帧重复导入
    _handled = true;
    Navigator.pop(context, value);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(title: const Text('扫码导入')),
      body: Stack(
        alignment: Alignment.center,
        children: [
          MobileScanner(controller: _scanner, onDetect: _onDetect),
          IgnorePointer(
            child: Container(
              width: 240,
              height: 240,
              decoration: BoxDecoration(
                border: Border.all(color: Colors.white70, width: 2),
                borderRadius: BorderRadius.circular(16),
              ),
            ),
          ),
          const Positioned(
            left: 24,
            right: 24,
            bottom: 48,
            child: Text(
              '对准订阅或节点二维码即可自动导入',
              textAlign: TextAlign.center,
              style: TextStyle(color: Colors.white70),
            ),
          ),
        ],
      ),
    );
  }
}

class HomePage extends StatelessWidget {
  const HomePage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final hasVpnTunnels = controller.tunnels.any((item) => item.mode == 'vpn');
    return ListView(
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 18),
      children: [
        StatusCard(controller: controller),
        const SizedBox(height: 2),
        HomeToolRow(controller: controller),
        if (hasVpnTunnels) ...[
          const SizedBox(height: 8),
          VpnTunnelCard(controller: controller),
        ],
        const SizedBox(height: 8),
        if (controller.subscriptions.isEmpty)
          const SoftCard(
            child: EmptyState(
              icon: Icons.rss_feed_outlined,
              title: '暂无订阅',
              subtitle: '点击右上角 + 添加订阅，或左上角扫码导入',
            ),
          )
        else
          for (final source in controller.subscriptions) ...[
            SubscriptionGroupCard(controller: controller, source: source),
            const SizedBox(height: 10),
          ],
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Text(
            '点击订阅可以折叠和展开子节点。圆点代表默认节点，连接中点击其他节点会直接切换。',
            textAlign: TextAlign.center,
            style: TextStyle(fontSize: 12, color: secondaryTextColor(context)),
          ),
        ),
      ],
    );
  }
}

class DataPage extends StatelessWidget {
  const DataPage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return CustomScrollView(
      slivers: [
        SliverPadding(
          padding: const EdgeInsets.fromLTRB(14, 12, 14, 0),
          sliver: SliverList(
            delegate: SliverChildListDelegate.fixed([
              RuntimeStatusCard(controller: controller),
              const SizedBox(height: 12),
              Wrap(
                spacing: 12,
                runSpacing: 12,
                children: [
                  StatTile(
                    icon: Icons.upload_outlined,
                    label: '上传速度',
                    value: formatSpeed(controller.uploadSpeedBps),
                  ),
                  StatTile(
                    icon: Icons.download_outlined,
                    label: '下载速度',
                    value: formatSpeed(controller.downloadSpeedBps),
                  ),
                  StatTile(
                    icon: Icons.cloud_upload_outlined,
                    label: '上传流量',
                    value: formatBytes(controller.uploadTotalBytes),
                  ),
                  StatTile(
                    icon: Icons.cloud_download_outlined,
                    label: '下载流量',
                    value: formatBytes(controller.downloadTotalBytes),
                  ),
                ],
              ),
              const SizedBox(height: 12),
            ]),
          ),
        ),
        // 日志卡片占满剩余高度，日志为空或很少时不会在下方留出大片空白，
        // 日志变长后按内容自然增高并继续滚动
        SliverFillRemaining(
          hasScrollBody: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(14, 0, 14, 18),
            child: LogsPreviewCard(controller: controller),
          ),
        ),
      ],
    );
  }
}

class ShieldMark extends StatelessWidget {
  const ShieldMark({super.key, this.size = 68});

  final double size;

  @override
  Widget build(BuildContext context) {
    // 与首页其余条目一致的主题色线条图标
    return Icon(
      Icons.link_outlined,
      size: size * 0.9,
      color: Theme.of(context).colorScheme.primary,
    );
  }
}

class SoftCard extends StatelessWidget {
  const SoftCard({super.key, required this.child, this.padding});

  final Widget child;
  final EdgeInsetsGeometry? padding;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: padding ?? const EdgeInsets.all(18),
      decoration: BoxDecoration(
        color: softCardColor(context),
        borderRadius: BorderRadius.circular(16),
        boxShadow: [
          BoxShadow(
            color: const Color(
              0xff23406f,
            ).withValues(alpha: isDarkMode(context) ? 0.3 : 0.08),
            blurRadius: 28,
            offset: const Offset(0, 14),
          ),
        ],
      ),
      // ListTile 的水波纹绘制在最近的 Material 上，透明 Material 避免被背景色遮挡
      child: Material(type: MaterialType.transparency, child: child),
    );
  }
}

class StatusCard extends StatelessWidget {
  const StatusCard({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final connected = controller.activeSession != null;
    final node = controller.currentNode ?? controller.selectedNode;
    final accent = Theme.of(context).colorScheme.primary;
    return SoftCard(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 2),
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Row(
              children: [
                const SizedBox(width: 32, child: Center(child: ShieldMark(size: 32))),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        connected ? '已连接' : '未连接',
                        style: TextStyle(
                          fontWeight: FontWeight.w800,
                          fontSize: 15,
                          color: connected
                              ? const Color(0xff16c56f)
                              : primaryTextColor(context),
                        ),
                      ),
                      Text(
                        node == null
                            ? '请先添加订阅并选择节点'
                            : '${flagForNode(node)} ${node.displayName}'
                                  .trim(),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontSize: 12,
                          color: secondaryTextColor(context),
                        ),
                      ),
                    ],
                  ),
                ),
                Switch(
                  value: connected,
                  onChanged: controller.busy
                      ? null
                      : (_) => controller.toggleConnection(context),
                ),
              ],
            ),
          ),
          Divider(height: 1, color: subtleBorderColor(context)),
          InkWell(
            onTap: controller.busy
                ? null
                : () => showProxyModeSheet(context, controller),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 13),
              child: Row(
                children: [
                  SizedBox(
                    width: 32,
                    child: Center(
                      child: Icon(Icons.explore_outlined, color: accent),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      '路由模式',
                      style: TextStyle(
                        fontWeight: FontWeight.w700,
                        color: primaryTextColor(context),
                      ),
                    ),
                  ),
                  Text(
                    controller.proxyModeLabel,
                    style: TextStyle(color: secondaryTextColor(context)),
                  ),
                  Icon(Icons.chevron_right, color: secondaryTextColor(context)),
                ],
              ),
            ),
          ),
          Divider(height: 1, color: subtleBorderColor(context)),
          InkWell(
            onTap: controller.busy ? null : controller.testAllProxyNodes,
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 13),
              child: Row(
                children: [
                  SizedBox(
                    width: 32,
                    child: Center(
                      child: Icon(Icons.network_check_outlined, color: accent),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      '连通性测试',
                      style: TextStyle(
                        fontWeight: FontWeight.w700,
                        color: primaryTextColor(context),
                      ),
                    ),
                  ),
                  Text(
                    '测试',
                    style: TextStyle(
                      color: controller.busy
                          ? secondaryTextColor(context)
                          : accent,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class HomeToolRow extends StatelessWidget {
  const HomeToolRow({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final iconColor = secondaryTextColor(context);
    return Row(
      mainAxisAlignment: MainAxisAlignment.end,
      children: [
        IconButton(
          tooltip: '自动选择',
          onPressed: controller.busy
              ? null
              : () => controller.autoSelectNode(context),
          icon: Icon(Icons.alt_route, color: iconColor),
        ),
        IconButton(
          tooltip: '刷新全部订阅',
          onPressed: controller.busy
              ? null
              : controller.refreshAllSubscriptions,
          icon: Icon(Icons.refresh, color: iconColor),
        ),
        PopupMenuButton<String>(
          tooltip: '更多',
          icon: Icon(Icons.more_horiz, color: iconColor),
          onSelected: (value) {
            switch (value) {
              case 'test':
                controller.testAllProxyNodes();
              case 'speed':
                final node = controller.currentNode ?? controller.selectedNode;
                if (node != null) controller.speedTestProxyNode(node);
            }
          },
          itemBuilder: (context) => const [
            PopupMenuItem(value: 'test', child: Text('延迟测试')),
            PopupMenuItem(value: 'speed', child: Text('速度测试')),
          ],
        ),
      ],
    );
  }
}

Future<void> showProxyModeSheet(
  BuildContext context,
  AppController controller,
) async {
  const items = [
    ('rule', '规则模式', '按路由规则智能分流', Icons.rule),
    ('global', '全局模式', '全部流量走代理', Icons.public),
    ('direct', '直连模式', '全部流量直连', Icons.link_outlined),
  ];
  await showModalBottomSheet<void>(
    context: context,
    backgroundColor: softCardColor(context),
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (sheetContext) {
      final accent = Theme.of(sheetContext).colorScheme.primary;
      return SheetFrame(
        title: '路由模式',
        children: [
          for (final item in items)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: InkWell(
                borderRadius: BorderRadius.circular(12),
                onTap: () {
                  Navigator.pop(sheetContext);
                  controller.setProxyMode(item.$1);
                },
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 12,
                  ),
                  decoration: BoxDecoration(
                    color: controller.proxyMode == item.$1
                        ? activeRowColor(sheetContext)
                        : Colors.transparent,
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: Row(
                    children: [
                      SizedBox(
                        width: 32,
                        child: Center(child: Icon(item.$4, color: accent)),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              item.$2,
                              style: TextStyle(
                                fontWeight: FontWeight.w700,
                                fontSize: 15,
                                color: primaryTextColor(sheetContext),
                              ),
                            ),
                            const SizedBox(height: 2),
                            Text(
                              item.$3,
                              style: TextStyle(
                                fontSize: 12,
                                color: secondaryTextColor(sheetContext),
                              ),
                            ),
                          ],
                        ),
                      ),
                      if (controller.proxyMode == item.$1)
                        Icon(Icons.check_circle, color: accent),
                    ],
                  ),
                ),
              ),
            ),
        ],
      );
    },
  );
}

class VpnTunnelCard extends StatelessWidget {
  const VpnTunnelCard({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final vpnTunnels = controller.tunnels
        .where((item) => item.mode == 'vpn')
        .toList();
    if (vpnTunnels.isEmpty) {
      return const SizedBox.shrink();
    }
    return SoftCard(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Text(
                'SafeLink VPN',
                style: Theme.of(
                  context,
                ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w900),
              ),
              const Spacer(),
              Text(
                '${vpnTunnels.length} 条线路',
                style: TextStyle(
                  color: secondaryTextColor(context),
                  fontSize: 12,
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          for (final tunnel in vpnTunnels)
            VpnTunnelRow(tunnel: tunnel, controller: controller),
        ],
      ),
    );
  }
}

class VpnTunnelRow extends StatelessWidget {
  const VpnTunnelRow({
    super.key,
    required this.tunnel,
    required this.controller,
  });

  final TunnelConfig tunnel;
  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final session = controller.activeSession;
    final active =
        session?.kind == 'safelink-vpn' && session?.name == tunnel.name;
    final server = tunnel.forward.isNotEmpty ? tunnel.forward : tunnel.sshAddr;
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 3),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: active ? activeRowColor(context) : Colors.transparent,
        border: Border.all(
          color: active ? const Color(0xff2f6fff) : Colors.transparent,
        ),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Row(
        children: [
          Icon(
            active ? Icons.shield : Icons.shield_outlined,
            color: active
                ? const Color(0xff16c56f)
                : secondaryTextColor(context),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  tunnel.name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: primaryTextColor(context),
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (server.isNotEmpty)
                  Text(
                    server,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: secondaryTextColor(context),
                      fontSize: 12,
                    ),
                  ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          FilledButton.tonal(
            onPressed: controller.busy
                ? null
                : () => active
                      ? controller.stopRuntime()
                      : controller.startVpnTunnel(context, tunnel),
            child: Text(active ? '断开' : '连接'),
          ),
        ],
      ),
    );
  }
}

class SubscriptionGroupCard extends StatelessWidget {
  const SubscriptionGroupCard({
    super.key,
    required this.controller,
    required this.source,
  });

  final AppController controller;
  final SubscriptionSource source;

  @override
  Widget build(BuildContext context) {
    final nodes = controller.nodesOfSubscription(source);
    final collapsed = controller.collapsedSubscriptions.contains(source.id);
    final iconColor = secondaryTextColor(context);
    return SoftCard(
      padding: const EdgeInsets.fromLTRB(6, 4, 6, 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          InkWell(
            borderRadius: BorderRadius.circular(10),
            onTap: () => controller.toggleSubscriptionCollapsed(source.id),
            child: Padding(
              padding: const EdgeInsets.fromLTRB(6, 6, 0, 6),
              child: Row(
                children: [
                  Icon(
                    collapsed ? Icons.chevron_right : Icons.expand_more,
                    color: iconColor,
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          source.name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            fontWeight: FontWeight.w800,
                            fontSize: 15,
                            color: primaryTextColor(context),
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          source.lastError.isNotEmpty
                              ? source.lastError
                              : '${nodes.length} 个节点 · 更新 ${source.lastRefresh == null ? '-' : _timeText(source.lastRefresh!)}',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            fontSize: 12,
                            color: source.lastError.isNotEmpty
                                ? Theme.of(context).colorScheme.error
                                : iconColor,
                          ),
                        ),
                      ],
                    ),
                  ),
                  IconButton(
                    tooltip: '刷新订阅',
                    onPressed: controller.busy
                        ? null
                        : () => controller.refreshSubscription(source),
                    icon: Icon(Icons.refresh, color: iconColor),
                  ),
                  IconButton(
                    tooltip: '订阅详情',
                    onPressed: () =>
                        showSubscriptionInfoSheet(context, controller, source),
                    icon: Icon(Icons.info_outline, color: iconColor),
                  ),
                ],
              ),
            ),
          ),
          if (!collapsed)
            if (nodes.isEmpty)
              Padding(
                padding: const EdgeInsets.all(12),
                child: Text(
                  '该订阅暂无节点，点击刷新重新拉取',
                  style: TextStyle(fontSize: 12, color: iconColor),
                ),
              )
            else
              for (final node in nodes)
                NodeRow(
                  node: node,
                  controller: controller,
                  selected: controller.selectedNode?.id == node.id,
                  active: controller.currentNode?.name == node.name,
                ),
        ],
      ),
    );
  }
}

Future<void> showSubscriptionInfoSheet(
  BuildContext context,
  AppController controller,
  SubscriptionSource source,
) async {
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: softCardColor(context),
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (sheetContext) {
      final nodeCount = controller.nodesOfSubscription(source).length;
      final errorColor = Theme.of(sheetContext).colorScheme.error;
      return SheetFrame(
        title: source.name,
        children: [
          SheetInfoRow(
            icon: Icons.link,
            label: '订阅地址',
            value: source.url,
            maxLines: 2,
            trailing: IconButton(
              tooltip: '复制地址',
              visualDensity: VisualDensity.compact,
              icon: Icon(
                Icons.copy_all_outlined,
                size: 18,
                color: secondaryTextColor(sheetContext),
              ),
              onPressed: () async {
                await Clipboard.setData(ClipboardData(text: source.url));
                if (!sheetContext.mounted) return;
                ScaffoldMessenger.of(sheetContext)
                  ..hideCurrentSnackBar()
                  ..showSnackBar(
                    const SnackBar(content: Text('订阅地址已复制')),
                  );
              },
            ),
          ),
          SheetInfoRow(
            icon: Icons.hub_outlined,
            label: '节点数量',
            value: '$nodeCount 个',
          ),
          SheetInfoRow(
            icon: Icons.schedule,
            label: '最后更新',
            value: source.lastRefresh == null
                ? '尚未更新'
                : _dateTimeText(source.lastRefresh!),
          ),
          if (source.lastError.isNotEmpty)
            SheetInfoRow(
              icon: Icons.error_outline,
              label: '最近错误',
              value: source.lastError,
              maxLines: 3,
              valueColor: errorColor,
            ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: OutlinedButton.icon(
                  style: OutlinedButton.styleFrom(
                    foregroundColor: errorColor,
                    side: BorderSide(color: subtleBorderColor(sheetContext)),
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  onPressed: controller.busy
                      ? null
                      : () {
                          Navigator.pop(sheetContext);
                          controller.deleteSubscription(source);
                        },
                  icon: const Icon(Icons.delete_outline, size: 18),
                  label: const Text('删除'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: FilledButton.icon(
                  style: FilledButton.styleFrom(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  onPressed: controller.busy
                      ? null
                      : () {
                          Navigator.pop(sheetContext);
                          controller.refreshSubscription(source);
                        },
                  icon: const Icon(Icons.refresh, size: 18),
                  label: const Text('更新'),
                ),
              ),
            ],
          ),
        ],
      );
    },
  );
}

class SheetInfoRow extends StatelessWidget {
  const SheetInfoRow({
    super.key,
    required this.icon,
    required this.label,
    required this.value,
    this.maxLines = 1,
    this.valueColor,
    this.trailing,
  });

  final IconData icon;
  final String label;
  final String value;
  final int maxLines;
  final Color? valueColor;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final accent = valueColor ?? Theme.of(context).colorScheme.primary;
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
      decoration: BoxDecoration(
        color: chipBackgroundColor(context),
        borderRadius: BorderRadius.circular(12),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(width: 32, child: Icon(icon, size: 20, color: accent)),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  label,
                  style: TextStyle(
                    fontSize: 12,
                    color: secondaryTextColor(context),
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  value,
                  maxLines: maxLines,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 13,
                    height: 1.4,
                    fontWeight: FontWeight.w700,
                    color: valueColor ?? primaryTextColor(context),
                  ),
                ),
              ],
            ),
          ),
          if (trailing != null) ...[const SizedBox(width: 8), trailing!],
        ],
      ),
    );
  }
}

class NodeRow extends StatelessWidget {
  const NodeRow({
    super.key,
    required this.node,
    required this.controller,
    required this.selected,
    required this.active,
  });

  final ProxyNode node;
  final AppController controller;
  final bool selected;
  final bool active;

  @override
  Widget build(BuildContext context) {
    final accent = Theme.of(context).colorScheme.primary;
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      // 点击选为默认节点；连接中则直接把会话切到该节点
      onTap: controller.busy
          ? null
          : () => controller.selectNode(context, node),
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 1),
        padding: const EdgeInsets.fromLTRB(12, 4, 0, 4),
        decoration: BoxDecoration(
          color: active ? activeRowColor(context) : Colors.transparent,
          borderRadius: BorderRadius.circular(10),
        ),
        child: Row(
          children: [
            Container(
              width: 8,
              height: 8,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                color: selected ? accent : Colors.transparent,
                border: selected
                    ? null
                    : Border.all(color: subtleBorderColor(context)),
              ),
            ),
            const SizedBox(width: 10),
            if (flagForNode(node).isNotEmpty) ...[
              Text(flagForNode(node), style: const TextStyle(fontSize: 18)),
              const SizedBox(width: 8),
            ],
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    node.displayName,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: primaryTextColor(context),
                      fontWeight: FontWeight.w600,
                      fontSize: 14,
                    ),
                  ),
                  Text(
                    protocolText(node.protocol),
                    style: TextStyle(
                      fontSize: 11,
                      color: secondaryTextColor(context),
                    ),
                  ),
                ],
              ),
            ),
            if (node.favorite)
              const Padding(
                padding: EdgeInsets.only(right: 6),
                child: Icon(Icons.star, size: 16, color: Color(0xffff9f0a)),
              ),
            Text(
              node.latencyMs == null ? '-' : '${node.latencyMs} ms',
              style: TextStyle(
                color: latencyColor(node.latencyMs),
                fontWeight: FontWeight.w700,
                fontSize: 12,
              ),
            ),
            IconButton(
              tooltip: '节点信息',
              onPressed: () => Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) =>
                      NodeDetailPage(node: node, controller: controller),
                ),
              ),
              icon: Icon(
                Icons.info_outline,
                size: 18,
                color: secondaryTextColor(context),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class NodeDetailPage extends StatefulWidget {
  const NodeDetailPage({
    super.key,
    required this.node,
    required this.controller,
  });

  final ProxyNode node;
  final AppController controller;

  @override
  State<NodeDetailPage> createState() => _NodeDetailPageState();
}

class _NodeDetailPageState extends State<NodeDetailPage> {
  bool _showSecrets = false;

  String get _subscriptionName {
    for (final source in widget.controller.subscriptions) {
      if (source.id == widget.node.subscriptionId) {
        return source.name;
      }
    }
    return '-';
  }

  String _mask(String value) => _showSecrets ? value : '••••••••';

  Widget _secretToggle(BuildContext context) => IconButton(
    visualDensity: VisualDensity.compact,
    tooltip: _showSecrets ? '隐藏敏感信息' : '显示敏感信息',
    onPressed: () => setState(() => _showSecrets = !_showSecrets),
    icon: Icon(
      _showSecrets ? Icons.visibility_off_outlined : Icons.visibility_outlined,
      size: 18,
      color: secondaryTextColor(context),
    ),
  );

  Widget _card(BuildContext context, List<Widget> rows) => SoftCard(
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
    child: Column(
      children: [
        for (var i = 0; i < rows.length; i++) ...[
          if (i > 0) Divider(height: 1, color: subtleBorderColor(context)),
          rows[i],
        ],
      ],
    ),
  );

  @override
  Widget build(BuildContext context) {
    final node = widget.node;
    final transportType = (node.transport['type']?.toString() ?? '').trim();
    final tlsEnabled = node.tls.isNotEmpty && node.tls['enabled'] != false;
    final sni = (node.tls['server_name'] ?? node.tls['sni'] ?? '')
        .toString()
        .trim();
    return Scaffold(
      appBar: AppBar(title: const Text('节点信息')),
      body: ListView(
        padding: const EdgeInsets.all(14),
        children: [
          _card(context, [
            DetailRow(label: '名称', value: node.name),
            DetailRow(label: '类型', value: protocolText(node.protocol)),
            DetailRow(label: '所属订阅', value: _subscriptionName),
          ]),
          const SizedBox(height: 12),
          _card(context, [
            DetailRow(label: '地址', value: node.server),
            DetailRow(label: '端口', value: '${node.port}'),
            if (node.method.isNotEmpty)
              DetailRow(label: '加密方式', value: node.method),
            if (node.password.isNotEmpty)
              DetailRow(
                label: '密码',
                value: _mask(node.password),
                trailing: _secretToggle(context),
              ),
            if (node.uuid.isNotEmpty)
              DetailRow(
                label: 'UUID',
                value: _mask(node.uuid),
                trailing: _secretToggle(context),
              ),
            if (node.alterId > 0)
              DetailRow(label: 'AlterId', value: '${node.alterId}'),
            if (node.security.isNotEmpty)
              DetailRow(label: '加密安全', value: node.security),
            if (node.flow.isNotEmpty)
              DetailRow(label: '流控', value: node.flow),
          ]),
          const SizedBox(height: 12),
          _card(context, [
            DetailRow(
              label: '传输方式',
              value: transportType.isEmpty ? 'none' : transportType,
            ),
            DetailRow(label: 'TLS', value: tlsEnabled ? '开启' : '关闭'),
            if (tlsEnabled && sni.isNotEmpty)
              DetailRow(label: 'SNI', value: sni),
            DetailRow(label: 'UDP 转发', value: node.udp ? '开启' : '关闭'),
          ]),
          const SizedBox(height: 12),
          _card(context, [
            DetailRow(
              label: '延迟',
              value: node.latencyMs == null ? '-' : '${node.latencyMs} ms',
            ),
            DetailRow(label: '速度', value: formatMbps(node.speedMbps)),
            DetailRow(label: '收藏', value: node.favorite ? '是' : '否'),
            if (node.lastTestedAt != null)
              DetailRow(label: '最后测试', value: _timeText(node.lastTestedAt!)),
          ]),
        ],
      ),
    );
  }
}

class DetailRow extends StatelessWidget {
  const DetailRow({
    super.key,
    required this.label,
    required this.value,
    this.trailing,
  });

  final String label;
  final String value;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: TextStyle(
              fontSize: 14,
              color: secondaryTextColor(context),
            ),
          ),
          const SizedBox(width: 24),
          Expanded(
            child: Text(
              value,
              textAlign: TextAlign.right,
              style: TextStyle(
                fontSize: 14,
                fontWeight: FontWeight.w600,
                color: primaryTextColor(context),
              ),
            ),
          ),
          if (trailing != null) ...[const SizedBox(width: 4), trailing!],
        ],
      ),
    );
  }
}

class LogsPreviewCard extends StatelessWidget {
  const LogsPreviewCard({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final logs = controller.logs.take(30).toList();
    return SoftCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Text(
                '连接日志',
                style: Theme.of(
                  context,
                ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w900),
              ),
              const Spacer(),
              TextButton(
                onPressed: controller.logs.isEmpty
                    ? null
                    : controller.clearLogs,
                child: const Text('清空'),
              ),
            ],
          ),
          const Divider(height: 22),
          if (logs.isEmpty)
            // 卡片由外层撑满剩余高度，占位内容在空白区域内居中
            const Expanded(
              child: Center(
                child: EmptyState(
                  icon: Icons.list_alt_outlined,
                  title: '暂无日志',
                  subtitle: '运行事件会显示在这里',
                ),
              ),
            )
          else
            for (final log in logs)
              InkWell(
                onTap: () => showLogDetailSheet(context, log),
                borderRadius: BorderRadius.circular(8),
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Row(
                    children: [
                      Text(
                        '•',
                        style: TextStyle(
                          color: log.levelColor(context),
                          fontSize: 18,
                        ),
                      ),
                      const SizedBox(width: 4),
                      SizedBox(
                        width: 128,
                        child: Text(
                          _dateTimeText(log.at),
                          style: const TextStyle(fontSize: 12),
                        ),
                      ),
                      SizedBox(
                        width: 42,
                        child: Text(
                          log.levelLabel,
                          style: TextStyle(
                            color: log.levelColor(context),
                            fontWeight: FontWeight.w800,
                            fontSize: 12,
                          ),
                        ),
                      ),
                      Expanded(
                        child: Text(
                          log.message,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
        ],
      ),
    );
  }
}

Future<void> showLogDetailSheet(BuildContext context, LogEntry log) async {
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: softCardColor(context),
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
    ),
    builder: (sheetContext) {
      return SheetFrame(
        title: '日志详情',
        children: [
          Row(
            children: [
              Text(
                log.levelLabel,
                style: TextStyle(
                  color: log.levelColor(sheetContext),
                  fontWeight: FontWeight.w800,
                  fontSize: 13,
                ),
              ),
              const SizedBox(width: 10),
              Text(
                _dateTimeText(log.at),
                style: TextStyle(
                  fontSize: 12,
                  color: secondaryTextColor(sheetContext),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: chipBackgroundColor(sheetContext),
              borderRadius: BorderRadius.circular(12),
            ),
            child: SelectableText(
              log.message,
              style: TextStyle(
                fontSize: 13,
                height: 1.5,
                color: primaryTextColor(sheetContext),
              ),
            ),
          ),
          const SizedBox(height: 12),
          Align(
            alignment: Alignment.centerRight,
            child: FilledButton.tonalIcon(
              onPressed: () async {
                await Clipboard.setData(ClipboardData(text: log.message));
                if (sheetContext.mounted) {
                  Navigator.of(sheetContext).pop();
                }
              },
              icon: const Icon(Icons.copy_all_outlined, size: 18),
              label: const Text('复制内容'),
            ),
          ),
        ],
      );
    },
  );
}

class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key, required this.controller});

  final AppController controller;

  Widget _sectionLabel(BuildContext context, String text) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 6, 4, 8),
      child: Text(
        text,
        style: TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w600,
          color: secondaryTextColor(context),
        ),
      ),
    );
  }

  void _toast(BuildContext context, String message) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 18),
      children: [
        _sectionLabel(context, '通用'),
        RuntimeCard(
          icon: Icons.dark_mode_outlined,
          title: '外观',
          subtitle: '当前：${AppController.themeModeLabel(controller.themeMode)}',
          trailing: DropdownButton<String>(
            value: controller.themeMode,
            underline: const SizedBox.shrink(),
            items: const [
              DropdownMenuItem(value: 'system', child: Text('跟随系统')),
              DropdownMenuItem(value: 'light', child: Text('浅色')),
              DropdownMenuItem(value: 'dark', child: Text('深色')),
            ],
            onChanged: controller.busy
                ? null
                : (value) {
                    if (value != null) {
                      controller.setThemeMode(value);
                    }
                  },
          ),
        ),
        const SizedBox(height: 12),
        RuntimeCard(
          icon: Icons.verified_user_outlined,
          title: '订阅证书',
          subtitle: controller.allowInsecureSubscriptionCerts
              ? '允许不受信任证书'
              : '严格校验证书',
          trailing: Switch(
            value: controller.allowInsecureSubscriptionCerts,
            onChanged: controller.busy
                ? null
                : controller.setAllowInsecureSubscriptionCerts,
          ),
        ),
        const SizedBox(height: 16),
        _sectionLabel(context, '维护'),
        RuntimeCard(
          icon: Icons.sync_outlined,
          title: '刷新全部订阅',
          subtitle: '共 ${controller.subscriptions.length} 个订阅',
          trailing: FilledButton.tonal(
            onPressed: controller.busy || controller.subscriptions.isEmpty
                ? null
                : () async {
                    await controller.refreshAllSubscriptions();
                    if (context.mounted) {
                      _toast(context, '订阅刷新完成');
                    }
                  },
            child: const Text('刷新'),
          ),
        ),
        const SizedBox(height: 12),
        RuntimeCard(
          icon: Icons.speed_outlined,
          title: '清除测试结果',
          subtitle: '重置 ${controller.proxyNodes.length} 个节点的延迟与速度记录',
          trailing: FilledButton.tonal(
            onPressed: controller.busy || controller.proxyNodes.isEmpty
                ? null
                : () async {
                    await controller.clearTestResults();
                    if (context.mounted) {
                      _toast(context, '测试结果已清除');
                    }
                  },
            child: const Text('清除'),
          ),
        ),
        const SizedBox(height: 12),
        RuntimeCard(
          icon: Icons.delete_sweep_outlined,
          title: '清空连接日志',
          subtitle: '当前 ${controller.logs.length} 条记录',
          trailing: FilledButton.tonal(
            onPressed: controller.busy || controller.logs.isEmpty
                ? null
                : () async {
                    await controller.clearLogs();
                    if (context.mounted) {
                      _toast(context, '日志已清空');
                    }
                  },
            child: const Text('清空'),
          ),
        ),
        const SizedBox(height: 16),
        _sectionLabel(context, '关于'),
        const RuntimeCard(
          icon: Icons.info_outline,
          title: '应用版本',
          subtitle: 'SafeLink 3.0.0',
        ),
        const SizedBox(height: 12),
        RuntimeCard(
          icon: Icons.memory_outlined,
          title: '内核版本',
          subtitle: controller.coreVersionText,
        ),
        const SizedBox(height: 12),
        RuntimeCard(
          icon: Icons.phone_android_outlined,
          title: '设备信息',
          subtitle: controller.deviceInfoText,
        ),
      ],
    );
  }
}

class RuntimeStatusCard extends StatelessWidget {
  const RuntimeStatusCard({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final session = controller.activeSession;
    final node = controller.currentNode ?? controller.selectedNode;
    return RuntimeCard(
      icon: session == null ? Icons.power_settings_new : Icons.check_circle,
      title: session?.name ?? '运行状态',
      subtitle: session?.kindLabel ?? '未连接',
      meta: session == null
          ? (node == null
                ? 'SafeLink 就绪，请先添加订阅并选择节点'
                : '点击卡片连接 ${node.displayName}')
          : '当前模式 ${controller.proxyModeLabel}',
      // 未连接时整张卡片充当开关，已连接时只保留右侧停止按钮，避免误触断开
      onTap: session != null || controller.busy
          ? null
          : () => controller.toggleConnection(context),
      trailing: session == null
          ? null
          : FilledButton.tonalIcon(
              onPressed: controller.busy ? null : controller.stopRuntime,
              icon: const Icon(Icons.stop),
              label: const Text('停止'),
            ),
    );
  }
}

Future<void> showSubscriptionDialog(
  BuildContext context,
  AppController controller,
) async {
  await showDialog<void>(
    context: context,
    builder: (dialogContext) => _SubscriptionDialog(controller: controller),
  );
}

class _SubscriptionDialog extends StatefulWidget {
  const _SubscriptionDialog({required this.controller});

  final AppController controller;

  @override
  State<_SubscriptionDialog> createState() => _SubscriptionDialogState();
}

class _SubscriptionDialogState extends State<_SubscriptionDialog> {
  // 控制器由 State 持有，待弹窗退场动画结束后随组件一起释放，
  // 避免关闭瞬间提前 dispose 触发框架断言（_dependents.isEmpty）
  final _url = TextEditingController();

  @override
  void dispose() {
    _url.dispose();
    super.dispose();
  }

  void _download() {
    final url = _url.text.trim();
    if (url.isEmpty) return;
    Navigator.pop(context);
    widget.controller.importSubscription(url);
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(24)),
      title: const Text(
        '从给定的 URL 下载配置…',
        textAlign: TextAlign.center,
        style: TextStyle(fontSize: 17, fontWeight: FontWeight.w800),
      ),
      content: TextField(
        controller: _url,
        autofocus: true,
        keyboardType: TextInputType.url,
        onSubmitted: (_) => _download(),
        decoration: InputDecoration(
          hintText: 'URL',
          filled: true,
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(12),
            borderSide: BorderSide.none,
          ),
          suffixIcon: IconButton(
            tooltip: '粘贴',
            icon: const Icon(Icons.content_paste),
            onPressed: () async {
              final clip = await Clipboard.getData(Clipboard.kTextPlain);
              final text = clip?.text?.trim();
              if (text != null && text.isNotEmpty) {
                _url.text = text;
              }
            },
          ),
        ),
      ),
      actionsAlignment: MainAxisAlignment.spaceEvenly,
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(onPressed: _download, child: const Text('下载')),
      ],
    );
  }
}

class SheetFrame extends StatelessWidget {
  const SheetFrame({super.key, required this.title, required this.children});

  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 16,
        right: 16,
        top: 16,
        bottom: MediaQuery.viewInsetsOf(context).bottom + 16,
      ),
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              title,
              style: Theme.of(context).textTheme.titleLarge?.copyWith(
                fontWeight: FontWeight.w800,
                color: primaryTextColor(context),
              ),
            ),
            const SizedBox(height: 16),
            ...children,
          ],
        ),
      ),
    );
  }
}

class RuntimeCard extends StatelessWidget {
  const RuntimeCard({
    super.key,
    required this.icon,
    required this.title,
    required this.subtitle,
    this.meta,
    this.trailing,
    this.actions = const [],
    this.onTap,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final String? meta;
  final Widget? trailing;
  final List<Widget> actions;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final accent = Theme.of(context).colorScheme.primary;
    // 内边距挪进 InkWell 内部，水波纹才能铺满整张卡片
    final content = Padding(
      padding: const EdgeInsets.all(14),
      child: Row(
        children: [
          Icon(icon, color: accent),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontWeight: FontWeight.w700,
                    fontSize: 15,
                    color: primaryTextColor(context),
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  subtitle,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 12,
                    color: secondaryTextColor(context),
                  ),
                ),
                if (meta != null && meta!.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 4),
                    child: Text(
                      meta!,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 11,
                        color: secondaryTextColor(context),
                      ),
                    ),
                  ),
              ],
            ),
          ),
          ?trailing,
          if (actions.isNotEmpty) ...actions,
        ],
      ),
    );
    return SoftCard(
      padding: EdgeInsets.zero,
      child: onTap == null
          ? content
          : InkWell(
              onTap: onTap,
              borderRadius: BorderRadius.circular(16),
              child: content,
            ),
    );
  }
}

class StatTile extends StatelessWidget {
  const StatTile({
    super.key,
    required this.icon,
    required this.label,
    required this.value,
  });

  final IconData icon;
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: (MediaQuery.sizeOf(context).width - 41) / 2,
      child: SoftCard(
        padding: const EdgeInsets.all(14),
        child: Row(
          children: [
            Icon(icon, color: Theme.of(context).colorScheme.primary),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    value,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 20,
                      fontWeight: FontWeight.w800,
                      color: primaryTextColor(context),
                    ),
                  ),
                  Text(
                    label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 12,
                      color: secondaryTextColor(context),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class EmptyState extends StatelessWidget {
  const EmptyState({
    super.key,
    required this.icon,
    required this.title,
    required this.subtitle,
  });

  final IconData icon;
  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 40),
      child: Column(
        children: [
          Icon(icon, size: 42),
          const SizedBox(height: 12),
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 4),
          Text(subtitle, textAlign: TextAlign.center),
        ],
      ),
    );
  }
}

// 机场按 Clash 约定用 subscription-userinfo 响应头返回套餐用量，各字段单位为字节，
// expire 是秒级 Unix 时间戳；服务商没给这个头时全部为 0。
class SubscriptionUsage {
  const SubscriptionUsage({
    this.upload = 0,
    this.download = 0,
    this.total = 0,
    this.expire = 0,
  });

  static const empty = SubscriptionUsage();

  final int upload;
  final int download;
  final int total;
  final int expire;

  bool get hasQuota => total > 0;
  int get used => upload + download;
  int get remaining => total > used ? total - used : 0;
  DateTime? get expireAt =>
      expire > 0 ? DateTime.fromMillisecondsSinceEpoch(expire * 1000) : null;

  static SubscriptionUsage fromHeader(String? raw) {
    if (raw == null || raw.trim().isEmpty) return empty;
    var upload = 0;
    var download = 0;
    var total = 0;
    var expire = 0;
    for (final field in raw.split(';')) {
      final index = field.indexOf('=');
      if (index <= 0) continue;
      final value = num.tryParse(field.substring(index + 1).trim())?.toInt();
      if (value == null || value < 0) continue;
      switch (field.substring(0, index).trim().toLowerCase()) {
        case 'upload':
          upload = value;
        case 'download':
          download = value;
        case 'total':
          total = value;
        case 'expire':
          expire = value;
      }
    }
    return SubscriptionUsage(
      upload: upload,
      download: download,
      total: total,
      expire: expire,
    );
  }

  Map<String, dynamic> toJson() => {
    'usage_upload': upload,
    'usage_download': download,
    'usage_total': total,
    'usage_expire': expire,
  };

  static SubscriptionUsage fromJson(Map<String, dynamic> json) =>
      SubscriptionUsage(
        upload: _int(json['usage_upload']),
        download: _int(json['usage_download']),
        total: _int(json['usage_total']),
        expire: _int(json['usage_expire']),
      );
}

class SubscriptionSource {
  SubscriptionSource({
    required this.id,
    required this.name,
    required this.url,
    this.kind = 'proxy',
    this.enabled = true,
    this.lastError = '',
    this.lastRefresh,
    this.usage = SubscriptionUsage.empty,
  });

  final String id;
  String name;
  String url;
  String kind;
  bool enabled;
  String lastError;
  DateTime? lastRefresh;
  SubscriptionUsage usage;

  String get kindLabel => kind == 'vpn' ? 'SafeLink VPN' : '代理订阅';

  Map<String, dynamic> toJson() => {
    'id': id,
    'name': name,
    'url': url,
    'kind': kind,
    'enabled': enabled,
    'last_error': lastError,
    'last_refresh': lastRefresh?.toIso8601String(),
    ...usage.toJson(),
  };

  static SubscriptionSource fromJson(Map<String, dynamic> json) =>
      SubscriptionSource(
        id: json['id'] as String? ?? _id(),
        name: json['name'] as String? ?? '',
        url: json['url'] as String? ?? '',
        kind: json['kind'] as String? ?? 'proxy',
        enabled: json['enabled'] as bool? ?? true,
        lastError: json['last_error'] as String? ?? '',
        lastRefresh: DateTime.tryParse(json['last_refresh'] as String? ?? ''),
        usage: SubscriptionUsage.fromJson(json),
      );
}

class ProxyNode {
  ProxyNode({
    required this.id,
    required this.subscriptionId,
    required this.name,
    required this.protocol,
    required this.server,
    required this.port,
    this.method = '',
    this.password = '',
    this.uuid = '',
    this.alterId = 0,
    this.security = '',
    this.flow = '',
    this.udp = false,
    Map<String, dynamic>? tls,
    Map<String, dynamic>? transport,
    this.favorite = false,
    this.latencyMs,
    this.speedMbps,
    this.lastError = '',
    this.lastTestedAt,
  }) : tls = tls ?? const {},
       transport = transport ?? const {};

  final String id;
  final String subscriptionId;
  String name;
  String protocol;
  String server;
  int port;
  String method;
  String password;
  String uuid;
  int alterId;
  String security;
  String flow;
  bool udp;
  Map<String, dynamic> tls;
  Map<String, dynamic> transport;
  bool favorite;
  int? latencyMs;
  double? speedMbps;
  String lastError;
  DateTime? lastTestedAt;

  int get latencySortValue => latencyMs ?? 1 << 30;
  String get displayName {
    final parts = name.split('-');
    if (parts.length > 1 && parts.first.trim().isNotEmpty) {
      return parts.skip(1).join('-').trim().isEmpty
          ? name
          : parts.skip(1).join('-').trim();
    }
    return name;
  }

  Map<String, dynamic> toJson() => {
    'id': id,
    'subscription_id': subscriptionId,
    'name': name,
    'protocol': protocol,
    'server': server,
    'port': port,
    'method': method,
    'password': password,
    'uuid': uuid,
    'alter_id': alterId,
    'security': security,
    'flow': flow,
    'udp': udp,
    'tls': tls,
    'transport': transport,
    'favorite': favorite,
    'latency_ms': latencyMs,
    'speed_mbps': speedMbps,
    'last_error': lastError,
    'last_tested_at': lastTestedAt?.toIso8601String(),
  };

  Map<String, dynamic> toCoreJson() {
    final data = <String, dynamic>{
      'name': name,
      'protocol': protocol,
      'server': server,
      'port': port,
    };
    void putString(String key, String value) {
      if (value.isNotEmpty) {
        data[key] = value;
      }
    }

    putString('method', method);
    putString('password', password);
    putString('uuid', uuid);
    if (alterId > 0) {
      data['alter_id'] = alterId;
    }
    putString('security', security);
    putString('flow', flow);
    if (udp) {
      data['udp'] = true;
    }
    if (tls.isNotEmpty) {
      data['tls'] = tls;
    }
    if (transport.isNotEmpty) {
      data['transport'] = transport;
    }
    return data;
  }

  static ProxyNode fromJson(Map<String, dynamic> json) => ProxyNode(
    id: json['id'] as String? ?? _id(),
    subscriptionId: json['subscription_id'] as String? ?? '',
    name: json['name'] as String? ?? '',
    protocol: json['protocol'] as String? ?? json['type'] as String? ?? '',
    server: json['server'] as String? ?? '',
    port: _int(json['port'] ?? json['server_port']),
    method: json['method'] as String? ?? json['cipher'] as String? ?? '',
    password: json['password'] as String? ?? '',
    uuid: json['uuid'] as String? ?? '',
    alterId: _int(json['alter_id'] ?? json['alterId']),
    security: json['security'] as String? ?? '',
    flow: json['flow'] as String? ?? '',
    udp: json['udp'] as bool? ?? false,
    tls: _map(json['tls']),
    transport: _map(json['transport']),
    favorite: json['favorite'] as bool? ?? false,
    latencyMs: json['latency_ms'] as int?,
    speedMbps: _nullableDouble(json['speed_mbps']),
    lastError: json['last_error'] as String? ?? '',
    lastTestedAt: DateTime.tryParse(json['last_tested_at'] as String? ?? ''),
  );

  static ProxyNode fromMap(
    SubscriptionSource source,
    Map<String, dynamic> map,
  ) {
    return ProxyNode(
      id: _id(),
      subscriptionId: source.id,
      name:
          '${source.name}-${map['name'] ?? map['tag'] ?? map['server'] ?? 'node'}',
      protocol: map['protocol'] as String? ?? map['type'] as String? ?? '',
      server: map['server'] as String? ?? '',
      port: _int(map['port'] ?? map['server_port']),
      method: map['method'] as String? ?? map['cipher'] as String? ?? '',
      password: map['password'] as String? ?? '',
      uuid: map['uuid'] as String? ?? '',
      alterId: _int(map['alter_id'] ?? map['alterId']),
      security: map['security'] as String? ?? '',
      flow: map['flow'] as String? ?? '',
      udp: map['udp'] as bool? ?? false,
      tls: _map(map['tls']),
      transport: _transportFromMap(map),
    );
  }

  static ProxyNode? fromUri(SubscriptionSource source, String line) {
    if (line.isEmpty || line.startsWith('#')) return null;
    final uri = Uri.tryParse(line);
    if (uri == null || uri.scheme.isEmpty) return null;
    if (!{
      'ss',
      'vmess',
      'vless',
      'trojan',
      'hysteria2',
      'tuic',
      'anytls',
    }.contains(uri.scheme)) {
      return null;
    }
    final name = uri.fragment.isEmpty
        ? uri.host
        : Uri.decodeComponent(uri.fragment);
    return ProxyNode(
      id: _id(),
      subscriptionId: source.id,
      name: '${source.name}-$name',
      protocol: uri.scheme,
      server: uri.host,
      port: uri.port,
      password: uri.userInfo,
    );
  }

  static Map<String, dynamic> _map(Object? value) {
    if (value is Map) {
      return Map<String, dynamic>.from(value);
    }
    return const {};
  }

  static Map<String, dynamic> _transportFromMap(Map<String, dynamic> map) {
    final existing = _map(map['transport']);
    if (existing.isNotEmpty) {
      return existing;
    }
    final type = map['network'] as String? ?? map['type'] as String? ?? '';
    final path = map['path'] as String? ?? '';
    final host = map['host'] as String? ?? '';
    final transport = <String, dynamic>{};
    if (type.isNotEmpty && type != map['protocol']) {
      transport['type'] = type;
    }
    if (path.isNotEmpty) {
      transport['path'] = path;
    }
    if (host.isNotEmpty) {
      transport['host'] = host;
    }
    return transport;
  }
}

class ProxyRule {
  ProxyRule({
    required this.id,
    required this.name,
    required this.type,
    required this.value,
    required this.outbound,
    this.enabled = true,
  });

  static const currentVersion = 1;
  static const typeDomain = 'domain';
  static const typeDomainSuffix = 'domain_suffix';
  static const typeDomainKeyword = 'domain_keyword';
  static const typeIPCIDR = 'ip_cidr';
  static const typeRuleSet = 'rule_set';
  static const outboundSelected = 'selected';
  static const outboundDirect = 'direct';
  static const outboundBlock = 'block';

  static const typeOptions = [
    typeDomainSuffix,
    typeDomain,
    typeDomainKeyword,
    typeIPCIDR,
    typeRuleSet,
  ];
  static const outboundOptions = [
    outboundDirect,
    outboundSelected,
    outboundBlock,
  ];

  final String id;
  String name;
  String type;
  String value;
  String outbound;
  bool enabled;

  String get typeLabel => typeText(type);
  String get outboundLabel => outboundText(outbound);
  IconData get outboundIcon => switch (normalizeOutbound(outbound)) {
    outboundSelected => Icons.public,
    outboundBlock => Icons.block,
    _ => Icons.verified_user_outlined,
  };

  Map<String, dynamic> toJson() => {
    'id': id,
    'name': name,
    'type': type,
    'value': value,
    'outbound': outbound,
    'enabled': enabled,
  };

  ProxyRule copy() => ProxyRule(
    id: id,
    name: name,
    type: type,
    value: value,
    outbound: outbound,
    enabled: enabled,
  );

  ProxyRule normalized() => ProxyRule(
    id: id.trim().isEmpty ? _id() : id.trim(),
    name: name.trim().isEmpty ? outboundText(outbound) : name.trim(),
    type: normalizeType(type),
    value: normalizeType(type) == typeRuleSet
        ? value.trim().toLowerCase()
        : value.trim(),
    outbound: normalizeOutbound(outbound),
    enabled: enabled,
  );

  static ProxyRule fromJson(Map<String, dynamic> json) => ProxyRule(
    id: json['id'] as String? ?? _id(),
    name: json['name'] as String? ?? '',
    type: json['type'] as String? ?? typeDomainSuffix,
    value: json['value'] as String? ?? '',
    outbound: json['outbound'] as String? ?? outboundDirect,
    enabled: json['enabled'] as bool? ?? true,
  );

  static ProxyRule draft(String outbound) {
    final normalizedOutbound = normalizeOutbound(outbound);
    final type = normalizedOutbound == outboundSelected
        ? typeDomainSuffix
        : typeIPCIDR;
    return ProxyRule(
      id: _id(),
      name: outboundText(normalizedOutbound),
      type: type,
      value: '',
      outbound: normalizedOutbound,
    );
  }

  static List<ProxyRule> defaultRules() => [
    _ipRule('lan-ipv4-this-network', 'IPv4 保留地址', '0.0.0.0/8'),
    _ipRule('lan-ipv4-private-10', 'IPv4 私有地址', '10.0.0.0/8'),
    _ipRule('lan-ipv4-carrier', '运营商内网', '100.64.0.0/10'),
    _ipRule('lan-ipv4-loopback', '本机地址', '127.0.0.0/8'),
    _ipRule('lan-ipv4-link-local', '链路本地', '169.254.0.0/16'),
    _ipRule('lan-ipv4-private-172', 'IPv4 私有地址', '172.16.0.0/12'),
    _ipRule('lan-ipv4-private-192', 'IPv4 私有地址', '192.168.0.0/16'),
    _ipRule('lan-ipv4-multicast', '组播地址', '224.0.0.0/4'),
    _ipRule('lan-ipv6-loopback', 'IPv6 本机地址', '::1/128'),
    _ipRule('lan-ipv6-unique-local', 'IPv6 私有地址', 'fc00::/7'),
    _ipRule('lan-ipv6-link-local', 'IPv6 链路本地', 'fe80::/10'),
    _ruleSet(
      'foreign-geosite',
      '国外域名',
      'geosite-geolocation-!cn',
      outboundSelected,
    ),
    _ruleSet('cn-geosite', '国内域名', 'geosite-geolocation-cn', outboundDirect),
    _ruleSet('cn-geoip', '国内 IP', 'geoip-cn', outboundDirect),
  ];

  static ProxyRule _ipRule(String id, String name, String value) => ProxyRule(
    id: id,
    name: name,
    type: typeIPCIDR,
    value: value,
    outbound: outboundDirect,
  );

  static ProxyRule _ruleSet(
    String id,
    String name,
    String value,
    String outbound,
  ) => ProxyRule(
    id: id,
    name: name,
    type: typeRuleSet,
    value: value,
    outbound: outbound,
  );

  static String normalizeType(String value) =>
      switch (value.trim().toLowerCase()) {
        typeDomain => typeDomain,
        typeDomainKeyword => typeDomainKeyword,
        typeIPCIDR || 'cidr' => typeIPCIDR,
        typeRuleSet || 'rule-set' || 'ruleset' => typeRuleSet,
        _ => typeDomainSuffix,
      };

  static String normalizeOutbound(String value) =>
      switch (value.trim().toLowerCase()) {
        outboundSelected || 'proxy' => outboundSelected,
        outboundBlock => outboundBlock,
        _ => outboundDirect,
      };

  static String typeText(String value) => switch (normalizeType(value)) {
    typeDomain => '完整域名',
    typeDomainKeyword => '域名关键词',
    typeIPCIDR => 'IP CIDR',
    typeRuleSet => '规则集',
    _ => '域名后缀',
  };

  static String outboundText(String value) =>
      switch (normalizeOutbound(value)) {
        outboundSelected => '代理',
        outboundBlock => '阻断',
        _ => '白名单',
      };

  static String placeholderFor(String type) => switch (normalizeType(type)) {
    typeDomain => 'example.com',
    typeDomainKeyword => 'google',
    typeIPCIDR => '192.168.0.0/16',
    typeRuleSet => 'geosite-geolocation-cn',
    _ => 'example.com',
  };
}

class TunnelConfig {
  TunnelConfig({
    required this.id,
    required this.name,
    required this.mode,
    this.subscriptionId = '',
    this.listen = '',
    this.forward = '',
    this.sshAddr = '',
    this.sshUser = '',
    this.password = '',
    this.tunSubnet = '10.8.0.2/24',
    this.dns = const ['1.1.1.1'],
    this.autoRoute = true,
    this.running = false,
  });

  final String id;
  final String subscriptionId;
  String name;
  String mode;
  String listen;
  String forward;
  String sshAddr;
  String sshUser;
  String password;
  String tunSubnet;
  List<String> dns;
  bool autoRoute;
  bool running;

  Map<String, dynamic> toJson() => {
    'id': id,
    'subscription_id': subscriptionId,
    'name': name,
    'mode': mode,
    'listen': listen,
    'forward': forward,
    'ssh_addr': sshAddr,
    'ssh_user': sshUser,
    'password': password,
    'tun_subnet': tunSubnet,
    'dns': dns,
    'auto_route': autoRoute,
    'running': running,
  };

  Map<String, dynamic> toCoreJson() => {
    'name': name,
    'mode': mode,
    'listen': listen,
    'forward': forward,
    'ssh': {'addr': sshAddr, 'user': sshUser, 'password': password},
    'tun': {'subnet': tunSubnet, 'dns': dns, 'auto_route': autoRoute},
  };

  static TunnelConfig fromJson(Map<String, dynamic> json) => TunnelConfig(
    id: json['id'] as String? ?? _id(),
    subscriptionId: json['subscription_id'] as String? ?? '',
    name: json['name'] as String? ?? '',
    mode: json['mode'] as String? ?? 'local',
    listen: json['listen'] as String? ?? '',
    forward: json['forward'] as String? ?? '',
    sshAddr: json['ssh_addr'] as String? ?? '',
    sshUser: json['ssh_user'] as String? ?? '',
    password: json['password'] as String? ?? '',
    tunSubnet: json['tun_subnet'] as String? ?? '10.8.0.2/24',
    dns:
        (json['dns'] as List?)?.whereType<String>().toList() ??
        const ['1.1.1.1'],
    autoRoute: json['auto_route'] as bool? ?? true,
    running: json['running'] as bool? ?? false,
  );

  static TunnelConfig fromSubscription(
    SubscriptionSource source,
    Map<String, dynamic> map,
  ) {
    final ssh = Map<String, dynamic>.from(map['ssh'] as Map? ?? {});
    final tun = Map<String, dynamic>.from(map['tun'] as Map? ?? {});
    return TunnelConfig(
      id: _id(),
      subscriptionId: source.id,
      name: '${source.name}-${map['name'] ?? 'vpn'}',
      mode: map['mode'] as String? ?? 'vpn',
      forward: map['forward'] as String? ?? '',
      sshAddr: ssh['addr'] as String? ?? '',
      sshUser: ssh['user'] as String? ?? '',
      password: ssh['password'] as String? ?? '',
      tunSubnet: tun['subnet'] as String? ?? '10.8.0.2/24',
      dns:
          (tun['dns'] as List?)?.whereType<String>().toList() ??
          const ['1.1.1.1'],
      autoRoute: tun['auto_route'] as bool? ?? true,
    );
  }
}

class RuntimeSession {
  RuntimeSession({
    required this.kind,
    required this.name,
    required this.startedAt,
  });

  final String kind;
  final String name;
  final DateTime startedAt;

  String get kindLabel {
    switch (kind) {
      case 'proxy-vpn':
        return '全局代理 VPN';
      case 'local-proxy':
        return '本地代理';
      case 'safelink-vpn':
        return 'SafeLink VPN';
      default:
        return kind;
    }
  }
}

class LogEntry {
  LogEntry({required this.level, required this.message, required this.at});

  final String level;
  final String message;
  final DateTime at;

  IconData get levelIcon {
    switch (level) {
      case 'error':
        return Icons.error_outline;
      case 'warn':
        return Icons.warning_amber_outlined;
      default:
        return Icons.info_outline;
    }
  }

  Color levelColor(BuildContext context) {
    switch (level) {
      case 'error':
        return Theme.of(context).colorScheme.error;
      case 'warn':
        return const Color(0xffb7791f);
      default:
        return Theme.of(context).colorScheme.primary;
    }
  }

  String get levelLabel {
    switch (level) {
      case 'error':
        return '错误';
      case 'warn':
        return '警告';
      default:
        return '信息';
    }
  }

  Map<String, dynamic> toJson() => {
    'level': level,
    'message': message,
    'at': at.toIso8601String(),
  };

  static LogEntry fromJson(Map<String, dynamic> json) => LogEntry(
    level: json['level'] as String? ?? 'info',
    message: json['message'] as String? ?? '',
    at: DateTime.tryParse(json['at'] as String? ?? '') ?? DateTime.now(),
  );
}

String _id() => DateTime.now().microsecondsSinceEpoch.toRadixString(16);

int _int(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  return int.tryParse('$value') ?? 0;
}

double _double(Object? value) {
  if (value is double) return value;
  if (value is num) return value.toDouble();
  return double.tryParse('$value') ?? 0;
}

double? _nullableDouble(Object? value) {
  final parsed = _double(value);
  return parsed <= 0 ? null : parsed;
}

String _timeText(DateTime time) {
  String two(int value) => value.toString().padLeft(2, '0');
  return '${two(time.month)}-${two(time.day)} ${two(time.hour)}:${two(time.minute)}:${two(time.second)}';
}

String _dateTimeText(DateTime time) {
  String two(int value) => value.toString().padLeft(2, '0');
  return '${time.year}-${two(time.month)}-${two(time.day)} ${two(time.hour)}:${two(time.minute)}:${two(time.second)}';
}

String formatBytes(int value) {
  if (value <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var size = value.toDouble();
  var unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return '${size.toStringAsFixed(size >= 10 || unit == 0 ? 0 : 1)} ${units[unit]}';
}

String formatSpeed(int value) => '${formatBytes(value)}/s';

String formatMbps(double? value) {
  if (value == null || value <= 0) return '-';
  return '${value.toStringAsFixed(value >= 10 ? 1 : 2)} Mbps';
}

String protocolText(String value) {
  final lower = value.toLowerCase();
  if (lower.contains('vmess')) return 'VMess';
  if (lower.contains('vless')) return 'VLESS';
  if (lower.contains('trojan')) return 'Trojan';
  if (lower.contains('hysteria')) return 'Hysteria';
  if (lower.contains('tuic')) return 'TUIC';
  return value.isEmpty ? 'Proxy' : value;
}

Color latencyColor(int? value) {
  if (value == null || value <= 0) {
    return const Color(0xff9aa4b5);
  }
  if (value <= 100) {
    return const Color(0xff18c56e);
  }
  if (value <= 160) {
    return const Color(0xffff8a00);
  }
  return const Color(0xffef4444);
}

String flagForNode(ProxyNode node) {
  // 节点名自带旗帜 emoji 时不再重复追加
  if (RegExp(r'[\u{1F1E6}-\u{1F1FF}]', unicode: true).hasMatch(node.name)) {
    return '';
  }
  final text = '${node.name} ${node.server}'.toLowerCase();
  if (text.contains('sg') ||
      text.contains('singapore') ||
      text.contains('新加坡')) {
    return '🇸🇬';
  }
  if (text.contains('us') ||
      text.contains('america') ||
      text.contains('los angeles') ||
      text.contains('美国')) {
    return '🇺🇸';
  }
  if (text.contains('de') ||
      text.contains('germany') ||
      text.contains('frankfurt') ||
      text.contains('德国')) {
    return '🇩🇪';
  }
  if (text.contains('gb') ||
      text.contains('uk') ||
      text.contains('london') ||
      text.contains('英国')) {
    return '🇬🇧';
  }
  if (text.contains('au') ||
      text.contains('australia') ||
      text.contains('sydney') ||
      text.contains('澳大利亚')) {
    return '🇦🇺';
  }
  if (text.contains('tw') ||
      text.contains('taiwan') ||
      text.contains('taipei') ||
      text.contains('台湾')) {
    return '🇹🇼';
  }
  if (text.contains('hk') ||
      text.contains('hong kong') ||
      text.contains('香港')) {
    return '🇭🇰';
  }
  return '🇯🇵';
}
