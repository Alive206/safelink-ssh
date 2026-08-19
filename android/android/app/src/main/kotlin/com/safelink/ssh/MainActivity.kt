package com.safelink.ssh

import android.content.Intent
import android.net.ConnectivityManager
import android.net.VpnService
import android.os.Build
import android.os.Handler
import android.os.Looper
import java.io.File
import java.util.concurrent.Executors
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.MethodChannel
import mobilecore.Mobilecore

class MainActivity : FlutterActivity() {
    private var pendingVpnPrepare: MethodChannel.Result? = null
    private lateinit var secureStore: SecureJsonStore
    private val core by lazy { CoreHolder.get(this) }

    // 测节点延迟这类内核调用要起内核再等结果，几秒是常态，留在主线程上必然 ANR。
    // 各次调用互不相干，用可扩容的线程池，别把批量测速串成一条队列。
    private val coreExecutor = Executors.newCachedThreadPool { runnable ->
        Thread(runnable, "safelink-core-call")
    }
    private val mainHandler = Handler(Looper.getMainLooper())

    // 动辄几十 KB 的 AES-GCM 加解密，成批写节点时会把主线程卡出掉帧。
    // 同一个 key 的先写后读必须保序，所以只用单线程队列。
    private val storeExecutor = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "safelink-secure-store")
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        secureStore = SecureJsonStore(this)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, NATIVE_CHANNEL)
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "prepareVpn" -> prepareVpn(result)
                    "startVpnSession" -> {
                        val args = call.arguments as? Map<*, *> ?: emptyMap<String, Any?>()
                        SafeLinkVpnService.start(
                            this,
                            args["kind"] as? String ?: "vpn",
                            args["name"] as? String ?: "SafeLink",
                            stringList(args["routes"], listOf("0.0.0.0/0")),
                            stringList(args["dns"], listOf("1.1.1.1", "8.8.8.8")),
                            args["tunnelJson"] as? String,
                            args["configJson"] as? String,
                            args["socksAddr"] as? String ?: "127.0.0.1:10808",
                            args["clashApiAddr"] as? String ?: "127.0.0.1:10810",
                        )
                        result.success(mapOf("state" to "starting"))
                    }
                    "stopVpnSession" -> {
                        SafeLinkVpnService.stop(this)
                        result.success(mapOf("state" to "stopping"))
                    }
                    "startLocalProxy" -> {
                        val args = call.arguments as? Map<*, *> ?: emptyMap<String, Any?>()
                        val configJson = args["configJson"] as? String
                        if (configJson.isNullOrBlank()) {
                            result.error("bad_request", "startLocalProxy requires configJson", null)
                            return@setMethodCallHandler
                        }
                        LocalProxyService.start(
                            this,
                            configJson,
                            args["node"] as? String ?: "proxy",
                            args["mode"] as? String ?: "rule",
                            args["socksAddr"] as? String ?: "127.0.0.1:10808",
                            args["clashApiAddr"] as? String ?: "127.0.0.1:10810",
                        )
                        result.success(mapOf("state" to "starting"))
                    }
                    "stopLocalProxy" -> {
                        LocalProxyService.stop(this)
                        result.success(mapOf("state" to "stopping"))
                    }
                    // status() 要向 clash API 拉流量，主线程上发起网络请求会被
                    // NetworkOnMainThreadException 打断，异常被内部吞掉后流量恒为 0，
                    // 所以查询必须排到后台线程，再回主线程应答。
                    "proxyStatus" -> coreExecutor.execute {
                        val outcome = runCatching { SingBoxRuntime.status() }
                        mainHandler.post {
                            outcome
                                .onSuccess { status -> result.success(status) }
                                .onFailure { error -> result.error("proxy_status_error", error.message, null) }
                        }
                    }
                    "reconcileRuntime" -> result.success(reconcileRuntime())
                    "secureRead" -> {
                        val key = call.argument<String>("key")
                        if (key == null) {
                            result.success(null)
                        } else {
                            storeExecutor.execute {
                                val outcome = runCatching { secureStore.read(key) }
                                mainHandler.post {
                                    outcome
                                        .onSuccess { value -> result.success(value) }
                                        .onFailure { error -> result.error("store_error", error.message, null) }
                                }
                            }
                        }
                    }
                    "secureWrite" -> {
                        val key = call.argument<String>("key")
                        val value = call.argument<String>("value") ?: ""
                        if (key == null) {
                            result.error("bad_request", "secureWrite requires key", null)
                        } else {
                            storeExecutor.execute {
                                val outcome = runCatching { secureStore.write(key, value) }
                                mainHandler.post {
                                    outcome
                                        .onSuccess { result.success(true) }
                                        .onFailure { error -> result.error("store_error", error.message, null) }
                                }
                            }
                        }
                    }
                    "secureRemove" -> {
                        val key = call.argument<String>("key")
                        if (key == null) {
                            result.success(true)
                        } else {
                            storeExecutor.execute {
                                secureStore.remove(key)
                                mainHandler.post { result.success(true) }
                            }
                        }
                    }
                    "getDeviceInfo" -> result.success(deviceInfo())
                    else -> result.notImplemented()
                }
            }
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CORE_CHANNEL)
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "call" -> {
                        val method = call.argument<String>("method")
                        val payload = call.argument<String>("payload") ?: "{}"
                        if (method == null) {
                            result.error("bad_request", "core call requires method", null)
                        } else {
                            coreExecutor.execute {
                                val outcome = runCatching { core.call(method, enrichCorePayload(payload)) }
                                // MethodChannel.Result 只能在主线程上回。
                                mainHandler.post {
                                    outcome
                                        .onSuccess { response -> result.success(response) }
                                        .onFailure { error -> result.error("core_error", error.message, null) }
                                }
                            }
                        }
                    }
                    "version" -> result.success(Mobilecore.Version)
                    else -> result.notImplemented()
                }
            }
        EventChannel(flutterEngine.dartExecutor.binaryMessenger, EVENT_CHANNEL)
            .setStreamHandler(object : EventChannel.StreamHandler {
                override fun onListen(arguments: Any?, events: EventChannel.EventSink?) {
                    EventBus.attach(events)
                }

                override fun onCancel(arguments: Any?) {
                    EventBus.attach(null)
                }
            })
    }

    override fun onDestroy() {
        coreExecutor.shutdown()
        storeExecutor.shutdown()
        super.onDestroy()
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == VPN_PREPARE_REQUEST) {
            pendingVpnPrepare?.success(mapOf("prepared" to (resultCode == RESULT_OK)))
            pendingVpnPrepare = null
        }
    }

    private fun prepareVpn(result: MethodChannel.Result) {
        val intent = VpnService.prepare(this)
        if (intent == null) {
            result.success(mapOf("prepared" to true))
            return
        }
        if (pendingVpnPrepare != null) {
            result.error("vpn_prepare_pending", "VPN permission request is already open", null)
            return
        }
        pendingVpnPrepare = result
        startActivityForResult(intent, VPN_PREPARE_REQUEST)
    }

    // 内核死了但 VPN 服务还持着 tun 时，0.0.0.0/0 会把全机流量引到一条死隧道，
    // 表现为“界面显示未连接但整机断网”。App 回到前台时对一次账，把残留的隧道拆掉。
    private fun reconcileRuntime(): Map<String, Any?> {
        val stale = SafeLinkVpnService.isRunning && !SingBoxRuntime.isRunning()
        if (stale) {
            SafeLinkVpnService.stop(this)
        }
        return mapOf("stale_tunnel_cleared" to stale)
    }

    private fun deviceInfo(): Map<String, Any?> = mapOf(
        "os" to "android",
        "sdk" to Build.VERSION.SDK_INT,
        "release" to Build.VERSION.RELEASE,
        "model" to Build.MODEL,
        "manufacturer" to Build.MANUFACTURER,
        "arch" to Build.SUPPORTED_ABIS.firstOrNull(),
    )

    private fun enrichCorePayload(payload: String): String {
        val trimmed = payload.ifBlank { "{}" }
        val json = org.json.JSONObject(trimmed)
        if (!json.has("core_path")) {
            json.put("core_path", File(applicationInfo.nativeLibraryDir, "libsing-box.so").absolutePath)
        }
        if (!json.has("work_dir")) {
            json.put("work_dir", File(filesDir, "proxy-tests").absolutePath)
        }
        if (!json.has("dns_servers")) {
            val servers = systemDnsServers()
            if (servers.isNotEmpty()) {
                json.put("dns_servers", org.json.JSONArray(servers))
            }
        }
        return json.toString()
    }

    // Android 没有 /etc/resolv.conf，sing-box 拿不到 DNS 就会去查 127.0.0.1:53 直接失败，
    // 所以把底层网络下发的 DNS 传给内核；园区网常常只放通自己的 DNS，公共 DNS 用不了。
    private fun systemDnsServers(): List<String> {
        val manager = getSystemService(ConnectivityManager::class.java) ?: return emptyList()
        val servers = LinkedHashSet<String>()
        val active = manager.activeNetwork
        val networks = buildList {
            if (active != null) add(active)
            addAll(manager.allNetworks)
        }
        for (network in networks) {
            val capabilities = manager.getNetworkCapabilities(network) ?: continue
            // 跳过 VPN（可能就是我们自己的隧道），只取真实底层网络的 DNS。
            if (capabilities.hasTransport(android.net.NetworkCapabilities.TRANSPORT_VPN)) continue
            val properties = manager.getLinkProperties(network) ?: continue
            for (address in properties.dnsServers) {
                address.hostAddress?.let { servers.add(it) }
            }
        }
        return servers.toList()
    }

    private fun stringList(value: Any?, fallback: List<String>): ArrayList<String> {
        val items = (value as? List<*>)?.mapNotNull { it as? String } ?: fallback
        return ArrayList(items)
    }

    private companion object {
        const val NATIVE_CHANNEL = "safelink/native"
        const val CORE_CHANNEL = "safelink/core"
        const val EVENT_CHANNEL = "safelink/events"
        const val VPN_PREPARE_REQUEST = 1562
    }
}
