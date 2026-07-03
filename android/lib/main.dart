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
  static const _storeKey = 'safelink_state_v1';
  final NativeBridge native;
  StreamSubscription<Map<String, dynamic>>? _eventSub;
  Timer? _sshPoll;
  Timer? _runtimePoll;
  bool _pollingSSH = false;
  bool _pollingRuntime = false;

  bool loaded = false;
  bool busy = false;
  int selectedIndex = 0;
  String proxyMode = 'rule';
  RuntimeSession? activeSession;
  final subscriptions = <SubscriptionSource>[];
  final proxyNodes = <ProxyNode>[];
  final tunnels = <TunnelConfig>[];
  final sshConnections = <SshConnection>[];
  final logs = <LogEntry>[];
  final terminalTabs = <TerminalTab>[];

  Future<void> load() async {
    final raw = await native.secureRead(_storeKey);
    if (raw != null && raw.isNotEmpty) {
      try {
        final data = jsonDecode(raw) as Map<String, dynamic>;
        proxyMode = data['proxy_mode'] as String? ?? 'rule';
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
        logs
          ..clear()
          ..addAll(_list(data['logs']).map(LogEntry.fromJson));
      } catch (error) {
        _log('error', '读取本地数据失败: $error');
      }
    }
    loaded = true;
    notifyListeners();
  }

  Future<void> save() async {
    final data = {
      'proxy_mode': proxyMode,
      'subscriptions': subscriptions.map((item) => item.toJson()).toList(),
      'proxy_nodes': proxyNodes.map((item) => item.toJson()).toList(),
      'tunnels': tunnels.map((item) => item.toJson()).toList(),
      'ssh_connections': sshConnections.map((item) => item.toJson()).toList(),
      'logs': logs.take(200).map((item) => item.toJson()).toList(),
    };
    await native.secureWrite(_storeKey, jsonEncode(data));
  }

  void selectPage(int index) {
    selectedIndex = index;
    notifyListeners();
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
      'socks_addr': _proxySocksAddr,
      'http_addr': _proxyHttpAddr,
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
        final watch = Stopwatch()..start();
        final socket = await Socket.connect(
          node.server,
          node.port,
          timeout: const Duration(seconds: 6),
        );
        socket.destroy();
        node.latencyMs = watch.elapsedMilliseconds;
        node.lastError = '';
        _log('info', '${node.name} 延迟 ${node.latencyMs} ms');
        await save();
        notifyListeners();
      },
      onError: (error) {
        node.latencyMs = null;
        node.lastError = error.toString();
      },
    );
  }

  Future<void> setProxyMode(String mode) async {
    proxyMode = mode;
    _log('info', '代理模式切换为 $mode');
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
      if (state == 'stopped' || state == 'error') {
        activeSession = null;
        final error =
            status?['last_error'] as String? ??
            status?['message'] as String? ??
            '';
        if (state == 'error' && error.isNotEmpty) {
          _log('error', '${session.name}: $error');
        } else {
          _log('info', '${session.name}: $state');
        }
        notifyListeners();
      }
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
          SubscriptionsPage(controller: controller),
          TunnelsPage(controller: controller),
          TerminalPage(controller: controller),
          SettingsPage(controller: controller),
          LogsPage(controller: controller),
        ];
        return Scaffold(
          appBar: AppBar(
            title: const Text('SafeLink'),
            actions: [
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
                icon: Icon(Icons.rss_feed_outlined),
                selectedIcon: Icon(Icons.rss_feed),
                label: '订阅',
              ),
              NavigationDestination(
                icon: Icon(Icons.alt_route_outlined),
                selectedIcon: Icon(Icons.alt_route),
                label: '隧道',
              ),
              NavigationDestination(
                icon: Icon(Icons.terminal_outlined),
                selectedIcon: Icon(Icons.terminal),
                label: '终端',
              ),
              NavigationDestination(
                icon: Icon(Icons.tune_outlined),
                selectedIcon: Icon(Icons.tune),
                label: '设置',
              ),
              NavigationDestination(
                icon: Icon(Icons.list_alt_outlined),
                selectedIcon: Icon(Icons.list_alt),
                label: '日志',
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
    final session = controller.activeSession;
    final vpnTunnels = controller.tunnels
        .where((item) => item.mode == 'vpn')
        .toList();
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        SectionHeader(
          title: session == null ? '未连接' : session.name,
          subtitle: session?.kindLabel ?? 'SafeLink 就绪',
        ),
        const SizedBox(height: 12),
        Wrap(
          spacing: 12,
          runSpacing: 12,
          children: [
            StatTile(
              icon: Icons.hub,
              label: '代理节点',
              value: '${controller.proxyNodes.length}',
            ),
            StatTile(
              icon: Icons.rss_feed,
              label: '订阅',
              value: '${controller.subscriptions.length}',
            ),
            StatTile(
              icon: Icons.alt_route,
              label: 'SSH 隧道',
              value:
                  '${controller.tunnels.where((item) => item.mode != 'vpn').length}',
            ),
            StatTile(
              icon: Icons.shield,
              label: 'VPN',
              value: '${vpnTunnels.length}',
            ),
          ],
        ),
        const SizedBox(height: 16),
        if (session != null)
          FilledButton.icon(
            onPressed: controller.busy ? null : controller.stopRuntime,
            icon: const Icon(Icons.stop_circle_outlined),
            label: const Text('断开当前连接'),
          ),
        if (vpnTunnels.isNotEmpty) ...[
          const SizedBox(height: 16),
          const SectionHeader(title: 'SafeLink VPN', subtitle: 'VPN 订阅节点'),
          const SizedBox(height: 8),
          for (final tunnel in vpnTunnels)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: RuntimeCard(
                icon: Icons.shield_outlined,
                title: tunnel.name,
                subtitle: tunnel.forward,
                trailing: FilledButton.tonalIcon(
                  onPressed: controller.busy
                      ? null
                      : () => controller.startVpnTunnel(context, tunnel),
                  icon: const Icon(Icons.play_arrow),
                  label: const Text('启动'),
                ),
              ),
            ),
        ],
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
      padding: const EdgeInsets.all(16),
      children: [
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
        const SizedBox(height: 12),
        if (controller.proxyNodes.isEmpty)
          const EmptyState(
            icon: Icons.hub_outlined,
            title: '暂无节点',
            subtitle: '从订阅页导入代理订阅',
          ),
        for (final node in controller.proxyNodes)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: RuntimeCard(
              icon: Icons.language,
              title: node.name,
              subtitle: '${node.protocol}  ${node.server}:${node.port}',
              meta: node.latencyMs == null
                  ? node.lastError
                  : '${node.latencyMs} ms',
              actions: [
                IconButton(
                  tooltip: '测试',
                  onPressed: controller.busy
                      ? null
                      : () => controller.testProxyNode(node),
                  icon: const Icon(Icons.speed),
                ),
                IconButton(
                  tooltip: '本地代理',
                  onPressed: controller.busy
                      ? null
                      : () => controller.startProxyNode(
                          context,
                          node,
                          vpnMode: false,
                        ),
                  icon: const Icon(Icons.lan),
                ),
                IconButton.filled(
                  tooltip: '全局 VPN',
                  onPressed: controller.busy
                      ? null
                      : () => controller.startProxyNode(
                          context,
                          node,
                          vpnMode: true,
                        ),
                  icon: const Icon(Icons.shield),
                ),
              ],
            ),
          ),
      ],
    );
  }
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
    final session = controller.activeSession;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        RuntimeCard(
          icon: Icons.power_settings_new,
          title: session?.name ?? '运行状态',
          subtitle: session?.kindLabel ?? '未连接',
          trailing: session == null
              ? null
              : FilledButton.tonalIcon(
                  onPressed: controller.busy ? null : controller.stopRuntime,
                  icon: const Icon(Icons.stop),
                  label: const Text('停止'),
                ),
        ),
        const SizedBox(height: 12),
        RuntimeCard(
          icon: Icons.route_outlined,
          title: '规则路由',
          subtitle: '当前模式 ${controller.proxyMode}',
          trailing: DropdownButton<String>(
            value: controller.proxyMode,
            items: const [
              DropdownMenuItem(value: 'rule', child: Text('规则')),
              DropdownMenuItem(value: 'global', child: Text('全局')),
              DropdownMenuItem(value: 'direct', child: Text('直连')),
            ],
            onChanged: controller.busy || session != null
                ? null
                : (value) => controller.setProxyMode(value ?? 'rule'),
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
    this.latencyMs,
    this.lastError = '',
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
  int? latencyMs;
  String lastError;

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
    'latency_ms': latencyMs,
    'last_error': lastError,
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
    latencyMs: json['latency_ms'] as int?,
    lastError: json['last_error'] as String? ?? '',
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

String _timeText(DateTime time) {
  String two(int value) => value.toString().padLeft(2, '0');
  return '${two(time.month)}-${two(time.day)} ${two(time.hour)}:${two(time.minute)}:${two(time.second)}';
}
