import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

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
    final scheme = ColorScheme.fromSeed(
      seedColor: const Color(0xff0f766e),
      brightness: Brightness.light,
    );
    return MaterialApp(
      title: 'SafeLink',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: scheme,
        useMaterial3: true,
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
      ),
      home: SafeLinkShell(controller: controller),
    );
  }
}

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
    _sshPoll = Timer.periodic(const Duration(milliseconds: 500), (_) {
      _pollSSHEvents();
    });
    _runtimePoll = Timer.periodic(const Duration(seconds: 2), (_) {
      _pollRuntimeStatus();
    });
  }

  static const _proxySocksAddr = '127.0.0.1:10808';
  static const _proxyHttpAddr = '127.0.0.1:10809';
  static const _proxyClashAddr = '127.0.0.1:10810';
  static const _storeKey = 'safelink_state_v1';
  static const _settingsVersion = 2;
  final NativeBridge native;
  StreamSubscription<Map<String, dynamic>>? _eventSub;
  Timer? _sshPoll;
  Timer? _runtimePoll;
  bool _pollingSSH = false;
  bool _pollingRuntime = false;

  bool loaded = false;
  bool busy = false;
  bool allowInsecureSubscriptionCerts = true;
  int selectedIndex = 0;
  String nodeFilter = 'all';
  String proxyMode = 'rule';
  RuntimeSession? activeSession;
  int uploadSpeedBps = 0;
  int downloadSpeedBps = 0;
  int uploadTotalBytes = 0;
  int downloadTotalBytes = 0;
  final subscriptions = <SubscriptionSource>[];
  final proxyNodes = <ProxyNode>[];
  final tunnels = <TunnelConfig>[];
  final sshConnections = <SshConnection>[];
  final routeRules = <ProxyRule>[];
  final logs = <LogEntry>[];
  final terminalTabs = <TerminalTab>[];

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
        final settingsVersion = data['settings_version'] as int? ?? 0;
        allowInsecureSubscriptionCerts = settingsVersion < _settingsVersion
            ? true
            : data['allow_insecure_subscription_certs'] as bool? ?? true;
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
        sshConnections
          ..clear()
          ..addAll(_list(data['ssh_connections']).map(SshConnection.fromJson));
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
          ..addAll(_list(data['logs']).map(LogEntry.fromJson));
      } catch (error) {
        _log('error', '读取本地数据失败: $error');
      }
    }
    if (routeRules.isEmpty) {
      routeRules.addAll(ProxyRule.defaultRules());
    }
    loaded = true;
    notifyListeners();
  }

  Future<void> save() async {
    final data = {
      'settings_version': _settingsVersion,
      'proxy_mode': proxyMode,
      'node_filter': nodeFilter,
      'allow_insecure_subscription_certs': allowInsecureSubscriptionCerts,
      'subscriptions': subscriptions.map((item) => item.toJson()).toList(),
      'proxy_nodes': proxyNodes.map((item) => item.toJson()).toList(),
      'tunnels': tunnels.map((item) => item.toJson()).toList(),
      'ssh_connections': sshConnections.map((item) => item.toJson()).toList(),
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

  List<ProxyNode> get visibleProxyNodes {
    final nodes = switch (nodeFilter) {
      'favorite' => proxyNodes.where((item) => item.favorite).toList(),
      'auto' => List<ProxyNode>.from(
        proxyNodes,
      )..sort((a, b) => a.latencySortValue.compareTo(b.latencySortValue)),
      _ => List<ProxyNode>.from(proxyNodes),
    };
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

  Future<void> importSubscription(String name, String url) async {
    await _run('导入订阅', () async {
      final source = SubscriptionSource(
        id: _id(),
        name: name.trim(),
        url: url.trim(),
        enabled: true,
        lastRefresh: DateTime.now(),
      );
      subscriptions.add(source);
      await refreshSubscription(source);
    });
  }

  Future<void> refreshSubscription(SubscriptionSource source) async {
    await _run(
      '刷新订阅',
      () async {
        final body = await _fetchText(source.url);
        final parsed = await native.coreCall('parseSubscription', {
          'body': body,
        });
        final imported = parsed != null && parsed['ok'] == true
            ? _importCoreSubscription(source, parsed['data'])
            : _parseSubscription(source, body);
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
      await save();
      notifyListeners();
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

  Future<void> testProxyNode(ProxyNode node) async {
    await _run(
      '测试节点',
      () async {
        final result = await _coreTestResult('testProxyNode', node);
        _applyTestResult(node, result);
        _log('info', '${node.name} 延迟 ${node.latencyMs ?? '-'} ms');
        await save();
        notifyListeners();
      },
      onError: (error) {
        node.latencyMs = null;
        node.lastError = error.toString();
      },
    );
  }

  Future<void> speedTestProxyNode(ProxyNode node) async {
    await _run(
      '速度测试',
      () async {
        final result = await _coreTestResult('speedTestProxyNode', node);
        _applyTestResult(node, result);
        _log('info', '${node.name} 速度 ${formatMbps(node.speedMbps)}');
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
      for (final raw in results) {
        final result = Map<String, dynamic>.from(raw);
        final name =
            (result['node_name'] as String?) ??
            (result['NodeName'] as String?) ??
            '';
        final node = proxyNodes.where((item) => item.name == name).firstOrNull;
        if (node != null) {
          _applyTestResult(node, result);
        }
      }
      _log('info', '节点延迟测试完成');
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

  void toggleFavorite(ProxyNode node) {
    node.favorite = !node.favorite;
    save();
    notifyListeners();
  }

  Future<void> shareApp() async {
    await native.call('shareApp');
    _log('info', '已打开系统分享');
    notifyListeners();
  }

  void showScanComingSoon(BuildContext context) {
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('扫码导入将在后续版本支持')));
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

  Future<void> saveRouteRule(ProxyRule rule) async {
    final normalized = rule.normalized();
    final index = routeRules.indexWhere((item) => item.id == normalized.id);
    if (index >= 0) {
      routeRules[index] = normalized;
    } else {
      routeRules.add(normalized);
    }
    _log('info', '已保存路由规则 ${normalized.name}');
    await save();
    notifyListeners();
  }

  Future<void> removeRouteRule(ProxyRule rule) async {
    routeRules.removeWhere((item) => item.id == rule.id);
    _log('info', '已删除路由规则 ${rule.name}');
    await save();
    notifyListeners();
  }

  Future<void> setRouteRuleEnabled(ProxyRule rule, bool enabled) async {
    rule.enabled = enabled;
    await save();
    notifyListeners();
  }

  Future<void> resetRouteRules() async {
    routeRules
      ..clear()
      ..addAll(ProxyRule.defaultRules());
    _log('info', '已恢复默认路由规则');
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

  Future<void> saveTunnel(TunnelConfig tunnel) async {
    final index = tunnels.indexWhere((item) => item.id == tunnel.id);
    if (index >= 0) {
      tunnels[index] = tunnel;
    } else {
      tunnels.add(tunnel);
    }
    _log('info', '已保存隧道 ${tunnel.name}');
    await save();
    notifyListeners();
  }

  Future<void> toggleTunnel(TunnelConfig tunnel) async {
    await _run('切换 SSH 隧道', () async {
      if (tunnel.running) {
        final resp = await native.coreCall('stopTunnel', {'name': tunnel.name});
        if (resp == null || resp['ok'] != true) {
          throw StateError(resp?['message'] as String? ?? 'stop tunnel failed');
        }
        tunnel.running = false;
      } else {
        final resp = await native.coreCall('startTunnel', {
          'tunnel': tunnel.toCoreJson(),
        });
        if (resp == null || resp['ok'] != true) {
          throw StateError(
            resp?['message'] as String? ?? 'start tunnel failed',
          );
        }
        tunnel.running = true;
      }
      _log('info', '${tunnel.name} ${tunnel.running ? '已启动' : '已停止'}');
      await save();
      notifyListeners();
    });
  }

  Future<void> deleteTunnel(TunnelConfig tunnel) async {
    tunnels.removeWhere((item) => item.id == tunnel.id);
    _log('info', '已删除隧道 ${tunnel.name}');
    await save();
    notifyListeners();
  }

  Future<void> saveSshConnection(SshConnection conn) async {
    final index = sshConnections.indexWhere((item) => item.id == conn.id);
    if (index >= 0) {
      sshConnections[index] = conn;
    } else {
      sshConnections.add(conn);
    }
    _log('info', '已保存 SSH 连接 ${conn.name}');
    await save();
    notifyListeners();
  }

  Future<void> deleteSshConnection(SshConnection conn) async {
    sshConnections.removeWhere((item) => item.id == conn.id);
    _log('info', '已删除 SSH 连接 ${conn.name}');
    await save();
    notifyListeners();
  }

  Future<void> openTerminal(SshConnection conn) async {
    await _run('连接 SSH 终端', () async {
      final resp = await native.coreCall('createSSHSession', {
        'config': {
          'addr': conn.addr,
          'user': conn.user,
          'password': conn.password,
          'rows': 24,
          'cols': 80,
        },
      });
      if (resp == null || resp['ok'] != true) {
        throw StateError(
          resp?['message'] as String? ?? 'Go core is unavailable',
        );
      }
      final data = Map<String, dynamic>.from(resp['data'] as Map? ?? {});
      terminalTabs.add(
        TerminalTab(
          id: _id(),
          sessionId: data['session_id'] as String? ?? '',
          title: conn.name,
          connection: conn,
          buffer: 'Connected to ${conn.addr}\r\n',
          connected: true,
        ),
      );
      _log('info', '已打开 SSH 终端 ${conn.name}');
      notifyListeners();
    });
  }

  Future<void> sendTerminalInput(TerminalTab tab, String input) async {
    if (!tab.connected || tab.sessionId.isEmpty) {
      tab.buffer += '> $input\r\n';
      notifyListeners();
      return;
    }
    final resp = await native.coreCall('sendSSHInput', {
      'session_id': tab.sessionId,
      'data': '$input\n',
    });
    if (resp == null || resp['ok'] != true) {
      tab.buffer += '\r\n${resp?['message'] ?? 'send failed'}\r\n';
    }
    notifyListeners();
  }

  Future<void> closeTerminal(TerminalTab tab) async {
    if (tab.sessionId.isNotEmpty) {
      await native.coreCall('closeSSHSession', {'session_id': tab.sessionId});
    }
    terminalTabs.removeWhere((item) => item.id == tab.id);
    _log('info', '终端 ${tab.title} 已关闭');
    notifyListeners();
  }

  Future<void> _pollSSHEvents() async {
    if (_pollingSSH || terminalTabs.isEmpty) {
      return;
    }
    _pollingSSH = true;
    try {
      final resp = await native.coreCall('drainSSHEvents');
      if (resp == null || resp['ok'] != true) {
        return;
      }
      final data = Map<String, dynamic>.from(resp['data'] as Map? ?? {});
      final events = (data['events'] as List? ?? const []).whereType<Map>();
      var changed = false;
      for (final raw in events) {
        final event = Map<String, dynamic>.from(raw);
        final sessionId = event['session_id'] as String? ?? '';
        final tab = terminalTabs
            .where((item) => item.sessionId == sessionId)
            .firstOrNull;
        if (tab == null) {
          continue;
        }
        switch (event['type']) {
          case 'output':
            tab.buffer += event['data'] as String? ?? '';
            break;
          case 'closed':
            tab.connected = false;
            tab.buffer += '\r\n[closed] ${event['message'] ?? ''}\r\n';
            break;
          case 'error':
            tab.buffer += '\r\n[error] ${event['message'] ?? ''}\r\n';
            break;
        }
        changed = true;
      }
      if (changed) {
        notifyListeners();
      }
    } finally {
      _pollingSSH = false;
    }
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
      if (state == 'stopped' || state == 'error') {
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

  Future<void> clearLogs() async {
    logs.clear();
    await save();
    notifyListeners();
  }

  @override
  void dispose() {
    _sshPoll?.cancel();
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
    if (current == null ||
        current.kind == nextKind ||
        current.kind == 'local-proxy') {
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

  Future<String> _fetchText(String url) async {
    final uri = Uri.parse(url);
    final client = HttpClient();
    if (allowInsecureSubscriptionCerts) {
      client.badCertificateCallback = (cert, host, port) => true;
    }
    try {
      final request = await client
          .getUrl(uri)
          .timeout(const Duration(seconds: 20));
      final response = await request.close().timeout(
        const Duration(seconds: 20),
      );
      if (response.statusCode < 200 || response.statusCode >= 300) {
        throw HttpException('HTTP ${response.statusCode}', uri: uri);
      }
      return await response.transform(utf8.decoder).join();
    } on HandshakeException catch (error) {
      if (allowInsecureSubscriptionCerts) {
        rethrow;
      }
      throw StateError('订阅证书校验失败，可在设置中允许不受信任订阅证书后重试: $error');
    } finally {
      client.close(force: true);
    }
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
      final line = payload['line'] as String? ?? '';
      if (line.isNotEmpty) {
        _log('info', 'sing-box: $line');
        notifyListeners();
      }
      return;
    }
    if (type == 'vpn.status' || type == 'proxy.status') {
      final state = payload['state'] as String? ?? '';
      if (state == 'stopped' || state == 'error') {
        activeSession = null;
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
          NodesPage(controller: controller),
          MePage(controller: controller),
        ];
        return Scaffold(
          backgroundColor: const Color(0xfff4f8ff),
          appBar: AppBar(
            backgroundColor: const Color(0xfff4f8ff),
            surfaceTintColor: Colors.transparent,
            elevation: 0,
            title: const BrandTitle(),
            actions: [
              if (controller.selectedIndex == 0)
                IconButton(
                  tooltip: '添加订阅',
                  onPressed: controller.busy
                      ? null
                      : () => showSubscriptionSheet(context, controller),
                  icon: const Icon(Icons.add),
                ),
              if (controller.selectedIndex == 2)
                IconButton(
                  tooltip: '扫码导入',
                  onPressed: () => controller.showScanComingSoon(context),
                  icon: const Icon(Icons.qr_code_scanner),
                ),
              if (controller.busy)
                const Padding(
                  padding: EdgeInsets.only(right: 16),
                  child: SizedBox.square(
                    dimension: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                ),
            ],
          ),
          body: SafeArea(child: pages[controller.selectedIndex]),
          bottomNavigationBar: NavigationBar(
            height: 72,
            backgroundColor: Colors.white,
            indicatorColor: const Color(0xffe8f0ff),
            selectedIndex: controller.selectedIndex,
            onDestinationSelected: controller.selectPage,
            destinations: const [
              NavigationDestination(
                icon: Icon(Icons.dashboard_outlined),
                selectedIcon: Icon(Icons.dashboard),
                label: '首页',
              ),
              NavigationDestination(
                icon: Icon(Icons.hub_outlined),
                selectedIcon: Icon(Icons.hub),
                label: '节点',
              ),
              NavigationDestination(
                icon: Icon(Icons.person_outline),
                selectedIcon: Icon(Icons.person),
                label: '我的',
              ),
            ],
          ),
        );
      },
    );
  }
}

class HomePage extends StatelessWidget {
  const HomePage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(18, 8, 18, 18),
      children: [
        ConnectionCard(controller: controller),
        const SizedBox(height: 16),
        NodeListCard(controller: controller, compact: true),
      ],
    );
  }
}

class NodesPage extends StatelessWidget {
  const NodesPage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(18, 8, 18, 18),
      children: [NodeListCard(controller: controller, compact: false)],
    );
  }
}

class MePage extends StatelessWidget {
  const MePage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.fromLTRB(18, 8, 18, 18),
      children: [
        SubscriptionManageCard(controller: controller),
        const SizedBox(height: 16),
        QuickActionsCard(controller: controller),
        const SizedBox(height: 16),
        LogsPreviewCard(controller: controller),
      ],
    );
  }
}

class BrandTitle extends StatelessWidget {
  const BrandTitle({super.key});

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        const ShieldMark(size: 34),
        const SizedBox(width: 10),
        Text(
          'SafeLink',
          style: Theme.of(context).textTheme.titleLarge?.copyWith(
            fontWeight: FontWeight.w900,
            color: const Color(0xff111827),
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
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0xff5f8cff), Color(0xff0058ff)],
        ),
        borderRadius: BorderRadius.circular(size * 0.24),
        boxShadow: [
          BoxShadow(
            color: const Color(0xff0b67ff).withValues(alpha: 0.24),
            blurRadius: size * 0.34,
            offset: Offset(0, size * 0.12),
          ),
        ],
      ),
      child: Icon(Icons.check_rounded, color: Colors.white, size: size * 0.58),
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
        color: Colors.white,
        borderRadius: BorderRadius.circular(16),
        boxShadow: [
          BoxShadow(
            color: const Color(0xff23406f).withValues(alpha: 0.08),
            blurRadius: 28,
            offset: const Offset(0, 14),
          ),
        ],
      ),
      child: child,
    );
  }
}

class ConnectionCard extends StatelessWidget {
  const ConnectionCard({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final connected = controller.activeSession != null;
    final node = controller.currentNode;
    final displayNode = node ?? controller.visibleProxyNodes.firstOrNull;
    return SoftCard(
      padding: const EdgeInsets.fromLTRB(20, 24, 20, 20),
      child: Column(
        children: [
          const ShieldMark(size: 86),
          const SizedBox(height: 14),
          Text(
            connected ? '已连接' : '未连接',
            style: Theme.of(context).textTheme.titleMedium?.copyWith(
              color: connected
                  ? const Color(0xff16c56f)
                  : const Color(0xff6b7280),
              fontWeight: FontWeight.w800,
            ),
          ),
          const SizedBox(height: 10),
          Text(
            displayNode == null
                ? '请选择节点'
                : '${flagForNode(displayNode)}  ${displayNode.displayName}',
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.titleMedium?.copyWith(
              fontWeight: FontWeight.w700,
              color: const Color(0xff26324a),
            ),
          ),
          const SizedBox(height: 28),
          Row(
            children: [
              Expanded(
                child: MetricText(
                  label: '延迟',
                  value: node?.latencyMs == null
                      ? '-'
                      : '${node!.latencyMs} ms',
                  highlight: true,
                ),
              ),
              const VerticalDivider(width: 1),
              Expanded(
                child: MetricText(
                  label: '速度',
                  value: connected && controller.downloadSpeedBps > 0
                      ? formatSpeed(controller.downloadSpeedBps)
                      : formatMbps(node?.speedMbps),
                ),
              ),
              const VerticalDivider(width: 1),
              Expanded(
                child: MetricText(
                  label: '流量',
                  value: formatBytes(controller.downloadTotalBytes),
                ),
              ),
            ],
          ),
          const SizedBox(height: 26),
          SizedBox(
            width: double.infinity,
            height: 52,
            child: FilledButton(
              style: FilledButton.styleFrom(
                backgroundColor: const Color(0xff0d63ff),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(10),
                ),
              ),
              onPressed: controller.busy
                  ? null
                  : connected
                  ? controller.stopRuntime
                  : displayNode == null
                  ? null
                  : () => controller.startProxyNode(
                      context,
                      displayNode,
                      vpnMode: false,
                    ),
              child: Text(connected ? '断开连接' : '连接节点'),
            ),
          ),
        ],
      ),
    );
  }
}

class MetricText extends StatelessWidget {
  const MetricText({
    super.key,
    required this.label,
    required this.value,
    this.highlight = false,
  });

  final String label;
  final String value;
  final bool highlight;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Text(
          label,
          style: const TextStyle(
            color: Color(0xff9aa4b5),
            fontWeight: FontWeight.w700,
          ),
        ),
        const SizedBox(height: 7),
        Text(
          value,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            color: highlight
                ? const Color(0xff16c56f)
                : const Color(0xff26324a),
            fontWeight: FontWeight.w800,
          ),
        ),
      ],
    );
  }
}

class NodeListCard extends StatelessWidget {
  const NodeListCard({
    super.key,
    required this.controller,
    required this.compact,
  });

  final AppController controller;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final nodes = controller.visibleProxyNodes;
    final visibleNodes = compact ? nodes.take(7).toList() : nodes;
    return SoftCard(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Text(
                '节点列表',
                style: Theme.of(
                  context,
                ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w900),
              ),
              const Spacer(),
              IconButton(
                tooltip: '筛选',
                onPressed: () {},
                icon: const Icon(Icons.filter_alt_outlined),
              ),
              PopupMenuButton<String>(
                tooltip: '更多',
                onSelected: (value) {
                  if (value == 'test') controller.testAllProxyNodes();
                },
                itemBuilder: (context) => const [
                  PopupMenuItem(value: 'test', child: Text('延迟测试')),
                ],
              ),
            ],
          ),
          const SizedBox(height: 8),
          NodeFilterBar(controller: controller),
          const SizedBox(height: 8),
          if (visibleNodes.isEmpty)
            const EmptyState(
              icon: Icons.hub_outlined,
              title: '暂无节点',
              subtitle: '点击右上角 + 添加订阅',
            )
          else
            for (final node in visibleNodes)
              NodeRow(
                node: node,
                controller: controller,
                active: controller.currentNode?.name == node.name,
              ),
        ],
      ),
    );
  }
}

class NodeFilterBar extends StatelessWidget {
  const NodeFilterBar({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    const items = [('all', '全部'), ('auto', '自动选择'), ('favorite', '收藏')];
    return Row(
      children: [
        for (final item in items)
          Expanded(
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 3),
              child: ChoiceChip(
                selected: controller.nodeFilter == item.$1,
                label: Center(child: Text(item.$2)),
                onSelected: (_) => controller.setNodeFilter(item.$1),
                selectedColor: const Color(0xffe6efff),
                labelStyle: TextStyle(
                  color: controller.nodeFilter == item.$1
                      ? const Color(0xff0d63ff)
                      : const Color(0xff374151),
                  fontWeight: FontWeight.w700,
                ),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
              ),
            ),
          ),
      ],
    );
  }
}

class NodeRow extends StatelessWidget {
  const NodeRow({
    super.key,
    required this.node,
    required this.controller,
    required this.active,
  });

  final ProxyNode node;
  final AppController controller;
  final bool active;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: controller.busy
          ? null
          : () => controller.startProxyNode(context, node, vpnMode: false),
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 3),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 12),
        decoration: BoxDecoration(
          color: active ? const Color(0xffeef5ff) : Colors.transparent,
          border: Border.all(
            color: active ? const Color(0xff2f6fff) : Colors.transparent,
          ),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Row(
          children: [
            Text(flagForNode(node), style: const TextStyle(fontSize: 20)),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                node.displayName,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                  color: Color(0xff26324a),
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
              decoration: BoxDecoration(
                color: const Color(0xfff2f5fa),
                borderRadius: BorderRadius.circular(6),
              ),
              child: Text(
                protocolText(node.protocol),
                style: const TextStyle(fontSize: 11, color: Color(0xff6b7280)),
              ),
            ),
            const SizedBox(width: 10),
            Text(
              node.latencyMs == null ? '-' : '${node.latencyMs} ms',
              style: TextStyle(
                color: latencyColor(node.latencyMs),
                fontWeight: FontWeight.w800,
                fontSize: 12,
              ),
            ),
            IconButton(
              tooltip: '收藏',
              onPressed: () => controller.toggleFavorite(node),
              icon: Icon(
                node.favorite ? Icons.star : Icons.star_border,
                color: const Color(0xff0d63ff),
              ),
            ),
            PopupMenuButton<String>(
              tooltip: '节点操作',
              onSelected: (value) {
                if (value == 'test') {
                  controller.testProxyNode(node);
                }
                if (value == 'speed') {
                  controller.speedTestProxyNode(node);
                }
                if (value == 'vpn') {
                  controller.startProxyNode(context, node, vpnMode: true);
                }
              },
              itemBuilder: (context) => const [
                PopupMenuItem(value: 'test', child: Text('延迟测试')),
                PopupMenuItem(value: 'speed', child: Text('速度测试')),
                PopupMenuItem(value: 'vpn', child: Text('全局 VPN')),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class SubscriptionManageCard extends StatelessWidget {
  const SubscriptionManageCard({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final source = controller.subscriptions.firstOrNull;
    return SoftCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Text(
                '订阅管理',
                style: Theme.of(
                  context,
                ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w900),
              ),
              const Spacer(),
              TextButton.icon(
                onPressed: controller.busy
                    ? null
                    : () => showSubscriptionSheet(context, controller),
                icon: const Icon(Icons.add),
                label: const Text('添加订阅'),
              ),
            ],
          ),
          const SizedBox(height: 14),
          if (source == null)
            const EmptyState(
              icon: Icons.rss_feed,
              title: '暂无订阅',
              subtitle: '添加订阅后自动导入节点',
            )
          else
            Container(
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(
                border: Border.all(color: const Color(0xffdbe4f0)),
                borderRadius: BorderRadius.circular(12),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          source.name,
                          style: const TextStyle(
                            fontWeight: FontWeight.w900,
                            fontSize: 16,
                          ),
                        ),
                      ),
                      const Chip(
                        label: Text('使用中'),
                        labelStyle: TextStyle(
                          color: Color(0xff15b66d),
                          fontWeight: FontWeight.w800,
                        ),
                        backgroundColor: Color(0xffe9fbf1),
                        visualDensity: VisualDensity.compact,
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  Text(
                    source.url,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(color: Color(0xff5f6b82)),
                  ),
                  const SizedBox(height: 12),
                  Text(
                    '最后更新： ${source.lastRefresh == null ? '-' : _dateTimeText(source.lastRefresh!)}',
                  ),
                  const SizedBox(height: 8),
                  Text(
                    '节点总数： ${controller.proxyNodes.where((item) => item.subscriptionId == source.id).length}',
                  ),
                  const SizedBox(height: 10),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      OutlinedButton(
                        onPressed: controller.busy
                            ? null
                            : () => controller.refreshSubscription(source),
                        child: const Text('更新'),
                      ),
                      PopupMenuButton<String>(
                        onSelected: (value) {
                          if (value == 'delete') {
                            controller.deleteSubscription(source);
                          }
                        },
                        itemBuilder: (context) => const [
                          PopupMenuItem(value: 'delete', child: Text('删除')),
                        ],
                      ),
                    ],
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class QuickActionsCard extends StatelessWidget {
  const QuickActionsCard({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final actions = [
      QuickAction(
        Icons.rocket_launch_outlined,
        '自动选择',
        () => controller.autoSelectNode(context),
      ),
      QuickAction(
        Icons.monitor_heart_outlined,
        '延迟测试',
        controller.testAllProxyNodes,
      ),
      QuickAction(Icons.speed_outlined, '速度测试', () {
        final node =
            controller.currentNode ?? controller.visibleProxyNodes.firstOrNull;
        if (node != null) controller.speedTestProxyNode(node);
      }),
      QuickAction(Icons.share_outlined, '分享应用', controller.shareApp),
    ];
    return SoftCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '常用功能',
            style: Theme.of(
              context,
            ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w900),
          ),
          const SizedBox(height: 18),
          Row(
            children: [
              for (final action in actions)
                Expanded(
                  child: InkWell(
                    onTap: controller.busy ? null : () => action.onTap(),
                    borderRadius: BorderRadius.circular(12),
                    child: Column(
                      children: [
                        Container(
                          width: 50,
                          height: 50,
                          decoration: BoxDecoration(
                            color: Colors.white,
                            border: Border.all(color: const Color(0xffdfe7f3)),
                            borderRadius: BorderRadius.circular(12),
                            boxShadow: [
                              BoxShadow(
                                color: const Color(
                                  0xff23406f,
                                ).withValues(alpha: 0.05),
                                blurRadius: 12,
                                offset: const Offset(0, 6),
                              ),
                            ],
                          ),
                          child: Icon(
                            action.icon,
                            color: const Color(0xff27364f),
                          ),
                        ),
                        const SizedBox(height: 9),
                        Text(
                          action.label,
                          style: const TextStyle(
                            fontWeight: FontWeight.w700,
                            fontSize: 12,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
            ],
          ),
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
    final logs = controller.logs.take(5).toList();
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
            const EmptyState(
              icon: Icons.list_alt_outlined,
              title: '暂无日志',
              subtitle: '运行事件会显示在这里',
            )
          else
            for (final log in logs)
              Padding(
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
        ],
      ),
    );
  }
}

class QuickAction {
  const QuickAction(this.icon, this.label, this.onTap);

  final IconData icon;
  final String label;
  final FutureOr<void> Function() onTap;
}

class SubscriptionsPage extends StatelessWidget {
  const SubscriptionsPage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        FilledButton.icon(
          onPressed: controller.busy
              ? null
              : () => showSubscriptionSheet(context, controller),
          icon: const Icon(Icons.add_link),
          label: const Text('添加订阅'),
        ),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: controller.busy
              ? null
              : controller.refreshAllSubscriptions,
          icon: const Icon(Icons.refresh),
          label: const Text('刷新全部'),
        ),
        const SizedBox(height: 16),
        if (controller.subscriptions.isEmpty)
          const EmptyState(
            icon: Icons.rss_feed_outlined,
            title: '暂无订阅',
            subtitle: '支持 SafeLink JSON、sing-box JSON 和常见 URI 列表',
          ),
        for (final source in controller.subscriptions)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: RuntimeCard(
              icon: source.kind == 'vpn'
                  ? Icons.shield_outlined
                  : Icons.rss_feed,
              title: source.name,
              subtitle: source.url,
              meta: source.lastError.isEmpty
                  ? source.kindLabel
                  : source.lastError,
              actions: [
                IconButton(
                  tooltip: '刷新',
                  onPressed: controller.busy
                      ? null
                      : () => controller.refreshSubscription(source),
                  icon: const Icon(Icons.refresh),
                ),
                IconButton(
                  tooltip: '删除',
                  onPressed: controller.busy
                      ? null
                      : () => controller.deleteSubscription(source),
                  icon: const Icon(Icons.delete_outline),
                ),
              ],
            ),
          ),
      ],
    );
  }
}

class TunnelsPage extends StatelessWidget {
  const TunnelsPage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final sshTunnels = controller.tunnels
        .where((item) => item.mode != 'vpn')
        .toList();
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        FilledButton.icon(
          onPressed: controller.busy
              ? null
              : () => showTunnelSheet(context, controller),
          icon: const Icon(Icons.add),
          label: const Text('新建 SSH 隧道'),
        ),
        const SizedBox(height: 16),
        if (sshTunnels.isEmpty)
          const EmptyState(
            icon: Icons.alt_route_outlined,
            title: '暂无 SSH 隧道',
            subtitle: 'local、remote、dynamic 三种模式',
          ),
        for (final tunnel in sshTunnels)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: RuntimeCard(
              icon: Icons.alt_route,
              title: tunnel.name,
              subtitle: '${tunnel.mode}  ${tunnel.listen} -> ${tunnel.forward}',
              meta: tunnel.running ? 'running' : 'stopped',
              actions: [
                IconButton(
                  tooltip: tunnel.running ? '停止' : '启动',
                  onPressed: controller.busy
                      ? null
                      : () => controller.toggleTunnel(tunnel),
                  icon: Icon(
                    tunnel.running
                        ? Icons.stop_circle_outlined
                        : Icons.play_circle_outline,
                  ),
                ),
                IconButton(
                  tooltip: '编辑',
                  onPressed: controller.busy
                      ? null
                      : () => showTunnelSheet(
                          context,
                          controller,
                          tunnel: tunnel,
                        ),
                  icon: const Icon(Icons.edit_outlined),
                ),
                IconButton(
                  tooltip: '删除',
                  onPressed: controller.busy
                      ? null
                      : () => controller.deleteTunnel(tunnel),
                  icon: const Icon(Icons.delete_outline),
                ),
              ],
            ),
          ),
      ],
    );
  }
}

class TerminalPage extends StatelessWidget {
  const TerminalPage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        FilledButton.icon(
          onPressed: controller.busy
              ? null
              : () => showSshSheet(context, controller),
          icon: const Icon(Icons.person_add_alt),
          label: const Text('保存 SSH 连接'),
        ),
        const SizedBox(height: 16),
        const SectionHeader(title: 'SSH 连接', subtitle: '账号配置'),
        const SizedBox(height: 8),
        if (controller.sshConnections.isEmpty)
          const EmptyState(
            icon: Icons.terminal_outlined,
            title: '暂无 SSH 连接',
            subtitle: '保存连接后可打开终端',
          ),
        for (final conn in controller.sshConnections)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: RuntimeCard(
              icon: Icons.dns_outlined,
              title: conn.name,
              subtitle: '${conn.user}@${conn.addr}',
              actions: [
                IconButton(
                  tooltip: '打开',
                  onPressed: controller.busy
                      ? null
                      : () => controller.openTerminal(conn),
                  icon: const Icon(Icons.open_in_new),
                ),
                IconButton(
                  tooltip: '编辑',
                  onPressed: controller.busy
                      ? null
                      : () => showSshSheet(context, controller, conn: conn),
                  icon: const Icon(Icons.edit_outlined),
                ),
                IconButton(
                  tooltip: '删除',
                  onPressed: controller.busy
                      ? null
                      : () => controller.deleteSshConnection(conn),
                  icon: const Icon(Icons.delete_outline),
                ),
              ],
            ),
          ),
        if (controller.terminalTabs.isNotEmpty) ...[
          const SizedBox(height: 12),
          const SectionHeader(title: '终端', subtitle: '会话'),
          const SizedBox(height: 8),
          for (final tab in controller.terminalTabs)
            Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: TerminalCard(controller: controller, tab: tab),
            ),
        ],
      ],
    );
  }
}

class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
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
      ],
    );
  }
}

class LogsPage extends StatelessWidget {
  const LogsPage({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        OutlinedButton.icon(
          onPressed: controller.logs.isEmpty ? null : controller.clearLogs,
          icon: const Icon(Icons.delete_sweep_outlined),
          label: const Text('清空日志'),
        ),
        const SizedBox(height: 12),
        if (controller.logs.isEmpty)
          const EmptyState(
            icon: Icons.list_alt_outlined,
            title: '暂无日志',
            subtitle: '运行事件会显示在这里',
          ),
        for (final log in controller.logs)
          ListTile(
            contentPadding: EdgeInsets.zero,
            leading: Icon(log.levelIcon, color: log.levelColor(context)),
            title: Text(log.message),
            subtitle: Text(_timeText(log.at)),
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
    return RuntimeCard(
      icon: session == null ? Icons.power_settings_new : Icons.check_circle,
      title: session?.name ?? '运行状态',
      subtitle: session?.kindLabel ?? '未连接',
      meta: session == null
          ? 'SafeLink 就绪'
          : '当前模式 ${controller.proxyModeLabel}',
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

class RouteRulesPanel extends StatelessWidget {
  const RouteRulesPanel({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return Card(
      color: colorScheme.surfaceContainerHighest.withValues(alpha: 0.48),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                const Icon(Icons.route_outlined),
                const SizedBox(width: 12),
                Expanded(
                  child: SectionHeader(
                    title: '规则路由',
                    subtitle: '当前模式 ${controller.proxyModeLabel}',
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(
                  value: 'rule',
                  icon: Icon(Icons.rule),
                  label: Text('规则'),
                ),
                ButtonSegment(
                  value: 'global',
                  icon: Icon(Icons.public),
                  label: Text('全局'),
                ),
                ButtonSegment(
                  value: 'direct',
                  icon: Icon(Icons.near_me),
                  label: Text('直连'),
                ),
              ],
              selected: {controller.proxyMode},
              onSelectionChanged: controller.busy
                  ? null
                  : (value) => controller.setProxyMode(value.first),
            ),
            if (controller.proxyMode == 'rule') ...[
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  OutlinedButton.icon(
                    onPressed: controller.busy
                        ? null
                        : () => showRouteRuleSheet(
                            context,
                            controller,
                            outbound: ProxyRule.outboundDirect,
                          ),
                    icon: const Icon(Icons.add),
                    label: const Text('白名单'),
                  ),
                  OutlinedButton.icon(
                    onPressed: controller.busy
                        ? null
                        : () => showRouteRuleSheet(
                            context,
                            controller,
                            outbound: ProxyRule.outboundSelected,
                          ),
                    icon: const Icon(Icons.add),
                    label: const Text('代理规则'),
                  ),
                  OutlinedButton.icon(
                    onPressed: controller.busy
                        ? null
                        : () => showRouteRuleSheet(
                            context,
                            controller,
                            outbound: ProxyRule.outboundBlock,
                          ),
                    icon: const Icon(Icons.block),
                    label: const Text('阻断规则'),
                  ),
                  TextButton.icon(
                    onPressed: controller.busy
                        ? null
                        : controller.resetRouteRules,
                    icon: const Icon(Icons.restore),
                    label: const Text('恢复默认'),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              if (controller.routeRules.isEmpty)
                const EmptyState(
                  icon: Icons.route_outlined,
                  title: '暂无路由规则',
                  subtitle: '添加白名单或代理规则后会用于规则模式',
                )
              else
                for (final rule in controller.routeRules)
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: RouteRuleTile(rule: rule, controller: controller),
                  ),
            ],
          ],
        ),
      ),
    );
  }
}

class RouteRuleTile extends StatelessWidget {
  const RouteRuleTile({
    super.key,
    required this.rule,
    required this.controller,
  });

  final ProxyRule rule;
  final AppController controller;

  @override
  Widget build(BuildContext context) {
    return RuntimeCard(
      icon: rule.outboundIcon,
      title: rule.name,
      subtitle: '${rule.typeLabel}  ${rule.value}',
      meta: rule.outboundLabel,
      trailing: Switch(
        value: rule.enabled,
        onChanged: controller.busy
            ? null
            : (value) => controller.setRouteRuleEnabled(rule, value),
      ),
      actions: [
        IconButton(
          tooltip: '编辑',
          onPressed: controller.busy
              ? null
              : () => showRouteRuleSheet(context, controller, rule: rule),
          icon: const Icon(Icons.edit_outlined),
        ),
        IconButton(
          tooltip: '删除',
          onPressed: controller.busy
              ? null
              : () => controller.removeRouteRule(rule),
          icon: const Icon(Icons.delete_outline),
        ),
      ],
    );
  }
}

class TerminalCard extends StatefulWidget {
  const TerminalCard({super.key, required this.controller, required this.tab});

  final AppController controller;
  final TerminalTab tab;

  @override
  State<TerminalCard> createState() => _TerminalCardState();
}

class _TerminalCardState extends State<TerminalCard> {
  final input = TextEditingController();

  @override
  void dispose() {
    input.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                const Icon(Icons.terminal),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    widget.tab.title,
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                ),
                IconButton(
                  tooltip: '关闭',
                  onPressed: () => widget.controller.closeTerminal(widget.tab),
                  icon: const Icon(Icons.close),
                ),
              ],
            ),
            Container(
              height: 180,
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: const Color(0xff101418),
                borderRadius: BorderRadius.circular(8),
              ),
              child: SingleChildScrollView(
                reverse: true,
                child: Text(
                  widget.tab.buffer,
                  style: const TextStyle(
                    color: Color(0xffd6e2ea),
                    fontFamily: 'monospace',
                    fontSize: 13,
                  ),
                ),
              ),
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: input,
                    decoration: const InputDecoration(labelText: '输入'),
                    onSubmitted: _send,
                  ),
                ),
                const SizedBox(width: 8),
                IconButton.filled(
                  tooltip: '发送',
                  onPressed: () => _send(input.text),
                  icon: const Icon(Icons.send),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  void _send(String value) {
    final text = value.trimRight();
    if (text.isEmpty) return;
    input.clear();
    widget.controller.sendTerminalInput(widget.tab, text);
  }
}

Future<void> showSubscriptionSheet(
  BuildContext context,
  AppController controller,
) async {
  final name = TextEditingController();
  final url = TextEditingController();
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (sheetContext) {
      return SheetFrame(
        title: '添加订阅',
        children: [
          TextField(
            controller: name,
            decoration: const InputDecoration(labelText: '名称'),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: url,
            decoration: const InputDecoration(labelText: 'URL'),
          ),
          const SizedBox(height: 16),
          FilledButton.icon(
            onPressed: () {
              Navigator.pop(sheetContext);
              controller.importSubscription(name.text, url.text);
            },
            icon: const Icon(Icons.add_link),
            label: const Text('导入'),
          ),
        ],
      );
    },
  );
  name.dispose();
  url.dispose();
}

Future<void> showRouteRuleSheet(
  BuildContext context,
  AppController controller, {
  ProxyRule? rule,
  String outbound = ProxyRule.outboundDirect,
}) async {
  final draft = rule?.copy() ?? ProxyRule.draft(outbound);
  final name = TextEditingController(text: draft.name);
  final value = TextEditingController(text: draft.value);
  var type = draft.type;
  var selectedOutbound = draft.outbound;
  var enabled = draft.enabled;
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (sheetContext) {
      return StatefulBuilder(
        builder: (context, setSheetState) {
          return SheetFrame(
            title: rule == null ? '添加路由规则' : '编辑路由规则',
            children: [
              TextField(
                controller: name,
                decoration: const InputDecoration(labelText: '名称'),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: type,
                decoration: const InputDecoration(labelText: '类型'),
                items: ProxyRule.typeOptions
                    .map(
                      (item) => DropdownMenuItem(
                        value: item,
                        child: Text(ProxyRule.typeText(item)),
                      ),
                    )
                    .toList(),
                onChanged: (next) {
                  if (next == null) return;
                  setSheetState(() {
                    type = next;
                    if (value.text.isEmpty) {
                      value.text = ProxyRule.placeholderFor(type);
                    }
                  });
                },
              ),
              const SizedBox(height: 12),
              TextField(
                controller: value,
                decoration: InputDecoration(
                  labelText: '匹配值',
                  hintText: ProxyRule.placeholderFor(type),
                ),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: selectedOutbound,
                decoration: const InputDecoration(labelText: '动作'),
                items: ProxyRule.outboundOptions
                    .map(
                      (item) => DropdownMenuItem(
                        value: item,
                        child: Text(ProxyRule.outboundText(item)),
                      ),
                    )
                    .toList(),
                onChanged: (next) => setSheetState(
                  () => selectedOutbound = next ?? selectedOutbound,
                ),
              ),
              const SizedBox(height: 8),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('启用'),
                value: enabled,
                onChanged: (next) => setSheetState(() => enabled = next),
              ),
              const SizedBox(height: 8),
              FilledButton.icon(
                onPressed: () {
                  final match = value.text.trim();
                  if (match.isEmpty) {
                    ScaffoldMessenger.of(
                      sheetContext,
                    ).showSnackBar(const SnackBar(content: Text('请填写匹配值')));
                    return;
                  }
                  Navigator.pop(sheetContext);
                  controller.saveRouteRule(
                    ProxyRule(
                      id: draft.id,
                      name: name.text.trim().isEmpty
                          ? ProxyRule.outboundText(selectedOutbound)
                          : name.text.trim(),
                      type: type,
                      value: match,
                      outbound: selectedOutbound,
                      enabled: enabled,
                    ),
                  );
                },
                icon: const Icon(Icons.save_outlined),
                label: const Text('保存'),
              ),
            ],
          );
        },
      );
    },
  );
  name.dispose();
  value.dispose();
}

Future<void> showTunnelSheet(
  BuildContext context,
  AppController controller, {
  TunnelConfig? tunnel,
}) async {
  final name = TextEditingController(text: tunnel?.name ?? '');
  final listen = TextEditingController(
    text: tunnel?.listen ?? '127.0.0.1:1080',
  );
  final forward = TextEditingController(text: tunnel?.forward ?? '');
  final sshAddr = TextEditingController(text: tunnel?.sshAddr ?? '');
  final sshUser = TextEditingController(text: tunnel?.sshUser ?? '');
  final password = TextEditingController(text: tunnel?.password ?? '');
  var mode = tunnel?.mode == 'vpn' ? 'local' : (tunnel?.mode ?? 'local');
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (sheetContext) {
      return StatefulBuilder(
        builder: (context, setSheetState) {
          return SheetFrame(
            title: tunnel == null ? '新建 SSH 隧道' : '编辑 SSH 隧道',
            children: [
              TextField(
                controller: name,
                decoration: const InputDecoration(labelText: '名称'),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: mode,
                decoration: const InputDecoration(labelText: '模式'),
                items: const [
                  DropdownMenuItem(value: 'local', child: Text('local')),
                  DropdownMenuItem(value: 'remote', child: Text('remote')),
                  DropdownMenuItem(value: 'dynamic', child: Text('dynamic')),
                ],
                onChanged: (value) =>
                    setSheetState(() => mode = value ?? 'local'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: listen,
                decoration: const InputDecoration(labelText: '监听'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: forward,
                decoration: const InputDecoration(labelText: '目标'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: sshAddr,
                decoration: const InputDecoration(labelText: 'SSH 地址'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: sshUser,
                decoration: const InputDecoration(labelText: 'SSH 用户'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: password,
                obscureText: true,
                decoration: const InputDecoration(labelText: '密码'),
              ),
              const SizedBox(height: 16),
              FilledButton.icon(
                onPressed: () {
                  Navigator.pop(sheetContext);
                  controller.saveTunnel(
                    TunnelConfig(
                      id: tunnel?.id ?? _id(),
                      name: name.text,
                      mode: mode,
                      listen: listen.text,
                      forward: forward.text,
                      sshAddr: sshAddr.text,
                      sshUser: sshUser.text,
                      password: password.text,
                    ),
                  );
                },
                icon: const Icon(Icons.save_outlined),
                label: const Text('保存'),
              ),
            ],
          );
        },
      );
    },
  );
  for (final item in [name, listen, forward, sshAddr, sshUser, password]) {
    item.dispose();
  }
}

Future<void> showSshSheet(
  BuildContext context,
  AppController controller, {
  SshConnection? conn,
}) async {
  final name = TextEditingController(text: conn?.name ?? '');
  final addr = TextEditingController(text: conn?.addr ?? '');
  final user = TextEditingController(text: conn?.user ?? '');
  final password = TextEditingController(text: conn?.password ?? '');
  await showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (sheetContext) {
      return SheetFrame(
        title: conn == null ? '保存 SSH 连接' : '编辑 SSH 连接',
        children: [
          TextField(
            controller: name,
            decoration: const InputDecoration(labelText: '名称'),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: addr,
            decoration: const InputDecoration(labelText: '地址'),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: user,
            decoration: const InputDecoration(labelText: '用户'),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: password,
            obscureText: true,
            decoration: const InputDecoration(labelText: '密码'),
          ),
          const SizedBox(height: 16),
          FilledButton.icon(
            onPressed: () {
              Navigator.pop(sheetContext);
              controller.saveSshConnection(
                SshConnection(
                  id: conn?.id ?? _id(),
                  name: name.text,
                  addr: addr.text,
                  user: user.text,
                  password: password.text,
                ),
              );
            },
            icon: const Icon(Icons.save_outlined),
            label: const Text('保存'),
          ),
        ],
      );
    },
  );
  for (final item in [name, addr, user, password]) {
    item.dispose();
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
            Text(title, style: Theme.of(context).textTheme.titleLarge),
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
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final String? meta;
  final Widget? trailing;
  final List<Widget> actions;

  @override
  Widget build(BuildContext context) {
    return Card(
      color: Theme.of(
        context,
      ).colorScheme.surfaceContainerHighest.withValues(alpha: 0.48),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Row(
          children: [
            Icon(icon),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 2),
                  Text(subtitle, maxLines: 2, overflow: TextOverflow.ellipsis),
                  if (meta != null && meta!.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 4),
                      child: Text(
                        meta!,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                    ),
                ],
              ),
            ),
            ?trailing,
            if (actions.isNotEmpty) ...actions,
          ],
        ),
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
      width: (MediaQuery.sizeOf(context).width - 44) / 2,
      child: Card(
        color: Theme.of(context).colorScheme.secondaryContainer,
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Row(
            children: [
              Icon(icon),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      value,
                      style: Theme.of(context).textTheme.headlineSmall,
                    ),
                    Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class SectionHeader extends StatelessWidget {
  const SectionHeader({super.key, required this.title, required this.subtitle});

  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: Theme.of(context).textTheme.headlineSmall),
        const SizedBox(height: 2),
        Text(subtitle, style: Theme.of(context).textTheme.bodyMedium),
      ],
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

class SubscriptionSource {
  SubscriptionSource({
    required this.id,
    required this.name,
    required this.url,
    this.kind = 'proxy',
    this.enabled = true,
    this.lastError = '',
    this.lastRefresh,
  });

  final String id;
  String name;
  String url;
  String kind;
  bool enabled;
  String lastError;
  DateTime? lastRefresh;

  String get kindLabel => kind == 'vpn' ? 'SafeLink VPN' : '代理订阅';

  Map<String, dynamic> toJson() => {
    'id': id,
    'name': name,
    'url': url,
    'kind': kind,
    'enabled': enabled,
    'last_error': lastError,
    'last_refresh': lastRefresh?.toIso8601String(),
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

class SshConnection {
  SshConnection({
    required this.id,
    required this.name,
    required this.addr,
    required this.user,
    required this.password,
  });

  final String id;
  String name;
  String addr;
  String user;
  String password;

  Map<String, dynamic> toJson() => {
    'id': id,
    'name': name,
    'addr': addr,
    'user': user,
    'password': password,
  };

  static SshConnection fromJson(Map<String, dynamic> json) => SshConnection(
    id: json['id'] as String? ?? _id(),
    name: json['name'] as String? ?? '',
    addr: json['addr'] as String? ?? '',
    user: json['user'] as String? ?? '',
    password: json['password'] as String? ?? '',
  );
}

class TerminalTab {
  TerminalTab({
    required this.id,
    required this.sessionId,
    required this.title,
    required this.connection,
    required this.buffer,
    required this.connected,
  });

  final String id;
  final String sessionId;
  final String title;
  final SshConnection connection;
  String buffer;
  bool connected;
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
