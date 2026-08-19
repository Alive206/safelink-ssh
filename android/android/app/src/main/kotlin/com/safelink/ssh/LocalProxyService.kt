package com.safelink.ssh

import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.IBinder
import androidx.core.content.ContextCompat
import java.util.concurrent.Executors

class LocalProxyService : Service() {
    // 启动内核要 exec 一个将近 60MB 的二进制并等端口就绪，不能占着主线程。
    private val worker = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "safelink-proxy-worker")
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when {
            // 系统重拉服务时 intent 为空，拿不到配置，直接收尾。
            intent == null -> worker.execute { stop() }
            intent.action == ACTION_STOP -> worker.execute { stop() }
            else -> {
                // 前台通知必须在 onStartCommand 返回前就位，否则会被系统当成启动超时杀掉。
                startForeground(
                    NotificationHelper.PROXY_NOTIFICATION_ID,
                    NotificationHelper.runtimeNotification(
                        this,
                        "SafeLink local proxy",
                        "Local SOCKS/HTTP proxy mode is active",
                    ),
                )
                worker.execute { start(intent) }
            }
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        worker.shutdown()
        super.onDestroy()
    }

    private fun start(intent: Intent?) {
        val configJson = intent?.getStringExtra(EXTRA_CONFIG_JSON)
        if (configJson.isNullOrBlank()) {
            EventBus.emit("proxy.status", mapOf("state" to "error", "message" to "missing sing-box config"))
            stopSelf()
            return
        }
        try {
            SingBoxRuntime.start(
                this,
                configJson,
                intent.getStringExtra(EXTRA_NODE_NAME) ?: "proxy",
                intent.getStringExtra(EXTRA_MODE) ?: "rule",
                intent.getStringExtra(EXTRA_SOCKS_ADDR) ?: DEFAULT_SOCKS_ADDR,
                intent.getStringExtra(EXTRA_CLASH_API_ADDR) ?: DEFAULT_CLASH_API_ADDR,
            )
        } catch (error: Throwable) {
            EventBus.emit(
                "proxy.status",
                mapOf("state" to "error", "mode" to "local", "message" to (error.message ?: "start failed")),
            )
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    private fun stop() {
        SingBoxRuntime.stop()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    companion object {
        private const val ACTION_STOP = "com.safelink.ssh.localproxy.STOP"
        private const val EXTRA_CONFIG_JSON = "config_json"
        private const val EXTRA_NODE_NAME = "node_name"
        private const val EXTRA_MODE = "mode"
        private const val EXTRA_SOCKS_ADDR = "socks_addr"
        private const val EXTRA_CLASH_API_ADDR = "clash_api_addr"
        private const val DEFAULT_SOCKS_ADDR = "127.0.0.1:10808"
        private const val DEFAULT_CLASH_API_ADDR = "127.0.0.1:10810"

        fun start(
            context: Context,
            configJson: String,
            nodeName: String,
            mode: String,
            socksAddr: String = DEFAULT_SOCKS_ADDR,
            clashAPIAddr: String = DEFAULT_CLASH_API_ADDR,
        ) {
            val intent = Intent(context, LocalProxyService::class.java)
                .putExtra(EXTRA_CONFIG_JSON, configJson)
                .putExtra(EXTRA_NODE_NAME, nodeName)
                .putExtra(EXTRA_MODE, mode)
                .putExtra(EXTRA_SOCKS_ADDR, socksAddr)
                .putExtra(EXTRA_CLASH_API_ADDR, clashAPIAddr)
            ContextCompat.startForegroundService(context, intent)
        }

        fun stop(context: Context) {
            val intent = Intent(context, LocalProxyService::class.java).setAction(ACTION_STOP)
            ContextCompat.startForegroundService(context, intent)
        }
    }
}
