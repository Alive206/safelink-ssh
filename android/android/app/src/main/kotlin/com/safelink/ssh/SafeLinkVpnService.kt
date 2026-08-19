package com.safelink.ssh

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import androidx.core.content.ContextCompat
import java.util.concurrent.Executors
import org.json.JSONObject

class SafeLinkVpnService : VpnService() {
    private var tun: ParcelFileDescriptor? = null
    private var currentKind: String = ""
    private val core by lazy { CoreHolder.get(this) }

    // sing-box 二进制接近 60MB，冷启动 exec 加上等端口就绪要好几秒，
    // 放在 onStartCommand 的主线程上必然 ANR，所以启停都排到单线程队列里执行。
    private val worker = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "safelink-vpn-worker")
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when {
            // 系统重拉服务时 intent 为空，没有任何配置，起隧道只会得到一条无人接流量的空隧道。
            intent == null -> worker.execute { stopVpn() }
            intent.action == ACTION_STOP -> worker.execute { stopVpn() }
            else -> {
                // 前台通知必须在 onStartCommand 返回前就位，不能等到 worker 线程里才补。
                startForeground(
                    NotificationHelper.VPN_NOTIFICATION_ID,
                    NotificationHelper.runtimeNotification(
                        this,
                        "SafeLink VPN",
                        "${intent.getStringExtra(EXTRA_SESSION_NAME) ?: "SafeLink"} is active",
                    ),
                )
                worker.execute { startVpn(intent) }
            }
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        stopVpn()
        worker.shutdown()
        super.onDestroy()
    }

    private fun startVpn(intent: Intent?) {
        val kind = intent?.getStringExtra(EXTRA_KIND) ?: "vpn"
        val sessionName = intent?.getStringExtra(EXTRA_SESSION_NAME) ?: "SafeLink"
        val routes = intent?.getStringArrayListExtra(EXTRA_ROUTES) ?: arrayListOf("0.0.0.0/0")
        val dns = intent?.getStringArrayListExtra(EXTRA_DNS) ?: arrayListOf("1.1.1.1", "8.8.8.8")
        val tunnelJson = intent?.getStringExtra(EXTRA_TUNNEL_JSON)
        val configJson = intent?.getStringExtra(EXTRA_CONFIG_JSON)
        val socksAddr = intent?.getStringExtra(EXTRA_SOCKS_ADDR) ?: DEFAULT_SOCKS_ADDR
        val clashAPIAddr = intent?.getStringExtra(EXTRA_CLASH_API_ADDR) ?: DEFAULT_CLASH_API_ADDR
        // 连接开着时切换节点会再来一次 start，必须先就地拆掉旧隧道：
        // 否则 SingBoxRuntime.start 抢占旧内核时会回调退出监听，把刚建好的新隧道又停掉。
        if (isRunning) {
            SingBoxRuntime.setExitListener(null)
            isRunning = false
            runCatching { core.call("stopVPN", "{}") }
            tun?.close()
            tun = null
        }
        currentKind = kind

        val notification = NotificationHelper.runtimeNotification(
            this,
            "SafeLink VPN",
            "$sessionName is active",
        )
        startForeground(NotificationHelper.VPN_NOTIFICATION_ID, notification)

        tun?.close()
        val builder = Builder()
            .setSession(sessionName)
            .setMtu(1500)
            .addAddress("10.88.0.2", 24)
        runCatching { builder.addDisallowedApplication(packageName) }
        dns.forEach { server ->
            runCatching { builder.addDnsServer(server) }
        }
        routes.forEach { route ->
            val parsed = parseCidr(route)
            if (parsed != null) {
                runCatching { builder.addRoute(parsed.first, parsed.second) }
            }
        }
        tun = builder.establish()
        if (kind == "proxy-vpn") {
            if (configJson.isNullOrBlank()) {
                EventBus.emit("vpn.status", mapOf("state" to "error", "message" to "missing sing-box config"))
                stopSelf()
                return
            }
            try {
                SingBoxRuntime.start(this, configJson, sessionName, "vpn", socksAddr, clashAPIAddr)
            } catch (error: Throwable) {
                EventBus.emit(
                    "vpn.status",
                    mapOf("state" to "error", "kind" to kind, "message" to (error.message ?: "proxy start failed")),
                )
                stopSelf()
                return
            }
            val fd = tun?.detachFd()
            tun = null
            if (fd == null || fd < 0) {
                EventBus.emit("vpn.status", mapOf("state" to "error", "message" to "failed to detach VPN fd"))
                SingBoxRuntime.stop()
                stopSelf()
                return
            }
            val parsedSocks = parseHostPort(socksAddr)
            val payload = JSONObject()
                .put("fd", fd)
                .put("name", sessionName)
                .put("socks_host", parsedSocks.first)
                .put("socks_port", parsedSocks.second)
                .put("udp", true)
            val response = core.call("startTun2SocksFromFD", payload.toString())
            val responseJson = JSONObject(response)
            if (!responseJson.optBoolean("ok")) {
                EventBus.emit(
                    "vpn.status",
                    mapOf("state" to "error", "kind" to kind, "message" to responseJson.optString("message")),
                )
                SingBoxRuntime.stop()
                stopSelf()
                return
            }
            // sing-box 启动后可能再崩或被本地代理抢占，而 tun 已经交给 tun2socks，
            // 必须联动拆隧道，否则 0.0.0.0/0 仍指向死隧道，UI 显示未连接但整机断网。
            // 抢占场景下新内核已经在跑，所以不能连带停内核。
            SingBoxRuntime.setExitListener {
                worker.execute { stopVpn(stopCore = false) }
            }
            isRunning = true
            EventBus.emit(
                "vpn.status",
                mapOf("state" to "running", "kind" to kind, "session" to sessionName, "core" to response),
            )
            return
        }
        if (kind == "safelink-vpn" && tunnelJson != null) {
            val fd = tun?.detachFd()
            tun = null
            if (fd == null || fd < 0) {
                EventBus.emit("vpn.status", mapOf("state" to "error", "message" to "failed to detach VPN fd"))
                stopSelf()
                return
            }
            val payload = JSONObject()
                .put("fd", fd)
                .put("tunnel", JSONObject(tunnelJson))
            val response = core.call("startVPNFromFD", payload.toString())
            val responseJson = JSONObject(response)
            if (!responseJson.optBoolean("ok")) {
                EventBus.emit(
                    "vpn.status",
                    mapOf("state" to "error", "kind" to kind, "message" to responseJson.optString("message")),
                )
                stopSelf()
                return
            }
            isRunning = true
            EventBus.emit(
                "vpn.status",
                mapOf("state" to "starting", "kind" to kind, "session" to sessionName, "core" to response),
            )
            return
        }
        isRunning = true
        EventBus.emit(
            "vpn.status",
            mapOf("state" to "running", "kind" to kind, "session" to sessionName),
        )
    }

    private fun stopVpn(stopCore: Boolean = true) {
        SingBoxRuntime.setExitListener(null)
        isRunning = false
        runCatching { core.call("stopVPN", "{}") }
        if (stopCore && currentKind == "proxy-vpn") {
            SingBoxRuntime.stop()
        }
        currentKind = ""
        tun?.close()
        tun = null
        EventBus.emit("vpn.status", mapOf("state" to "stopped"))
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun parseCidr(cidr: String): Pair<String, Int>? {
        val parts = cidr.split("/")
        if (parts.size != 2) {
            return null
        }
        val prefix = parts[1].toIntOrNull() ?: return null
        return parts[0] to prefix
    }

    private fun parseHostPort(addr: String): Pair<String, Int> {
        val index = addr.lastIndexOf(':')
        if (index <= 0 || index == addr.lastIndex) {
            return "127.0.0.1" to 10808
        }
        val host = addr.substring(0, index).trim().trim('[', ']')
        val port = addr.substring(index + 1).toIntOrNull() ?: 10808
        return host to port
    }

    companion object {
        // 服务是否正在持有隧道。App 重启后靠它跟内核状态对账，拆掉残留的死隧道。
        @Volatile
        var isRunning = false
            private set

        private const val ACTION_STOP = "com.safelink.ssh.vpn.STOP"
        private const val EXTRA_KIND = "kind"
        private const val EXTRA_SESSION_NAME = "session_name"
        private const val EXTRA_ROUTES = "routes"
        private const val EXTRA_DNS = "dns"
        private const val EXTRA_TUNNEL_JSON = "tunnel_json"
        private const val EXTRA_CONFIG_JSON = "config_json"
        private const val EXTRA_SOCKS_ADDR = "socks_addr"
        private const val EXTRA_CLASH_API_ADDR = "clash_api_addr"
        private const val DEFAULT_SOCKS_ADDR = "127.0.0.1:10808"
        private const val DEFAULT_CLASH_API_ADDR = "127.0.0.1:10810"

        fun start(
            context: Context,
            kind: String,
            sessionName: String,
            routes: ArrayList<String>,
            dns: ArrayList<String>,
            tunnelJson: String? = null,
            configJson: String? = null,
            socksAddr: String = DEFAULT_SOCKS_ADDR,
            clashAPIAddr: String = DEFAULT_CLASH_API_ADDR,
        ) {
            val intent = Intent(context, SafeLinkVpnService::class.java)
                .putExtra(EXTRA_KIND, kind)
                .putExtra(EXTRA_SESSION_NAME, sessionName)
                .putStringArrayListExtra(EXTRA_ROUTES, routes)
                .putStringArrayListExtra(EXTRA_DNS, dns)
                .putExtra(EXTRA_TUNNEL_JSON, tunnelJson)
                .putExtra(EXTRA_CONFIG_JSON, configJson)
                .putExtra(EXTRA_SOCKS_ADDR, socksAddr)
                .putExtra(EXTRA_CLASH_API_ADDR, clashAPIAddr)
            ContextCompat.startForegroundService(context, intent)
        }

        fun stop(context: Context) {
            val intent = Intent(context, SafeLinkVpnService::class.java).setAction(ACTION_STOP)
            ContextCompat.startForegroundService(context, intent)
        }
    }
}
