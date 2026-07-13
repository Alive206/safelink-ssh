package com.safelink.ssh

import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.IBinder
import androidx.core.content.ContextCompat

class LocalProxyService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> stop()
            else -> start(intent)
        }
        return START_STICKY
    }

    private fun start(intent: Intent?) {
        val notification = NotificationHelper.runtimeNotification(
            this,
            "SafeLink local proxy",
            "Local SOCKS/HTTP proxy mode is active",
        )
        startForeground(NotificationHelper.PROXY_NOTIFICATION_ID, notification)
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
