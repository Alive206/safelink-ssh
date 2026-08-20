package com.safelink.ssh

import android.content.Context
import java.io.File
import java.net.HttpURLConnection
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URL
import java.util.concurrent.TimeUnit
import org.json.JSONObject

object SingBoxRuntime {
    private val lock = Any()

    // 流量计数单独上锁：clash API 查询要走网络，最长 1.4s，
    // 占着 lock 会把内核启停一起拖住。
    private val trafficLock = Any()
    private val ansiPattern = Regex("\u001B\\[[0-9;]*[a-zA-Z]")
    private var process: Process? = null
    private var nodeName: String = ""
    private var mode: String = ""
    private var clashAPIAddr: String = ""
    private var startedAt: Long = 0L
    private var lastError: String = ""
    private var lastTrafficAt: Long = 0L
    private var lastUploadTotal: Long = 0L
    private var lastDownloadTotal: Long = 0L
    private var uploadSpeed: Long = 0L
    private var downloadSpeed: Long = 0L
    private var trafficError: String = ""

    private data class Traffic(
        val uploadTotal: Long,
        val downloadTotal: Long,
        val uploadSpeed: Long,
        val downloadSpeed: Long,
        val error: String,
    )

    // 内核失败时的原因只在它自己的输出里，留一段滑动窗口给错误提示用。
    private val recentLogs = ArrayDeque<String>()

    // sing-box 意外退出时的回调。tun 的 fd 已经交给 tun2socks，内核死了隧道还在，
    // 0.0.0.0/0 会一直指向一条死隧道导致整机断网，所以必须联动把 VPN 服务停掉。
    @Volatile
    private var exitListener: (() -> Unit)? = null

    fun setExitListener(listener: (() -> Unit)?) {
        exitListener = listener
    }

    fun isRunning(): Boolean = synchronized(lock) { process?.isAlive == true }

    // 内核的工作目录。cache_file 由 Go 侧写进配置里的绝对路径，两边必须指同一个地方，
    // 否则 rule_set 的 .srs 缓存每次都落在内核找不到的位置，等于白缓存。
    fun workDir(context: Context): File =
        File(context.applicationContext.filesDir, "sing-box").also { it.mkdirs() }

    fun start(
        context: Context,
        configJson: String,
        node: String,
        proxyMode: String,
        socksAddr: String,
        clashAddr: String,
    ): Map<String, Any?> {
        // 本地代理和 VPN 共用这一个内核实例，抢占前先让旧持有者收尾，
        // 否则 stopLocked 静默杀掉旧进程，VPN 服务毫不知情，留下一条没人接流量的隧道。
        val preempted = exitListener
        exitListener = null
        preempted?.invoke()
        synchronized(lock) {
            stopLocked(emitEvent = false)
            val appContext = context.applicationContext
            val workDir = workDir(appContext)
            val configFile = File(workDir, "config.json")
            configFile.writeText(configJson, Charsets.UTF_8)

            val binary = File(appContext.applicationInfo.nativeLibraryDir, "libsing-box.so")
            if (!binary.isFile) {
                lastError = "sing-box binary not found: ${binary.absolutePath}"
                throw IllegalStateException(lastError)
            }
            binary.setExecutable(true, false)

            try {
                synchronized(recentLogs) { recentLogs.clear() }
                val next = ProcessBuilder(binary.absolutePath, "run", "-c", configFile.absolutePath)
                    .directory(workDir)
                    .redirectErrorStream(true)
                    .start()
                process = next
                nodeName = node
                mode = proxyMode
                clashAPIAddr = clashAddr
                startedAt = System.currentTimeMillis()
                lastError = ""
                resetTraffic()

                pumpLogs(next)
                monitor(next)
                waitForReady(next, socksAddr)

                val status = statusLocked("running")
                EventBus.emit("proxy.status", status)
                return status
            } catch (error: Throwable) {
                if (lastError.isEmpty()) {
                    lastError = error.message ?: "failed to start sing-box"
                }
                stopLocked(emitEvent = false)
                throw error
            }
        }
    }

    fun stop(): Map<String, Any?> {
        synchronized(lock) {
            lastError = ""
            stopLocked(emitEvent = true)
            return statusLocked("stopped")
        }
    }

    fun status(): Map<String, Any?> {
        val addr = synchronized(lock) {
            val current = process
            if (current != null && current.isAlive) {
                clashAPIAddr
            } else {
                process = null
                null
            }
        }
        // 刷新流量必须在 lock 之外：这是一次真实的 HTTP 请求。
        if (!addr.isNullOrBlank()) {
            refreshTraffic(addr)
        }
        synchronized(lock) {
            return statusLocked(if (process?.isAlive == true) "running" else "stopped")
        }
    }

    private fun stopLocked(emitEvent: Boolean) {
        val current = process
        process = null
        if (current != null && current.isAlive) {
            current.destroy()
            if (!current.waitFor(2, TimeUnit.SECONDS)) {
                current.destroyForcibly()
                current.waitFor(1, TimeUnit.SECONDS)
            }
        }
        nodeName = ""
        mode = ""
        clashAPIAddr = ""
        startedAt = 0L
        resetTraffic()
        if (emitEvent) {
            EventBus.emit("proxy.status", statusLocked("stopped"))
        }
    }

    private fun statusLocked(state: String): Map<String, Any?> {
        val traffic = if (state == "running") trafficSnapshot() else Traffic(0L, 0L, 0L, 0L, "")
        return mapOf(
            "state" to state,
            "node" to nodeName,
            "mode" to mode,
            "started_at" to startedAt,
            "last_error" to lastError,
            "upload_total_bytes" to traffic.uploadTotal,
            "download_total_bytes" to traffic.downloadTotal,
            "upload_speed_bps" to traffic.uploadSpeed,
            "download_speed_bps" to traffic.downloadSpeed,
            "traffic_error" to traffic.error,
        )
    }

    private fun trafficSnapshot(): Traffic = synchronized(trafficLock) {
        Traffic(lastUploadTotal, lastDownloadTotal, uploadSpeed, downloadSpeed, trafficError)
    }

    private fun resetTraffic() {
        synchronized(trafficLock) {
            lastTrafficAt = 0L
            lastUploadTotal = 0L
            lastDownloadTotal = 0L
            uploadSpeed = 0L
            downloadSpeed = 0L
            trafficError = ""
        }
    }

    // clash API 的 /connections 带全局累计量，速率由相邻两次采样的差值算出。
    private fun refreshTraffic(addr: String) {
        val json = runCatching {
            val conn = URL("http://$addr/connections").openConnection() as HttpURLConnection
            conn.connectTimeout = 700
            conn.readTimeout = 700
            conn.requestMethod = "GET"
            val body = try {
                conn.inputStream.bufferedReader().use { it.readText() }
            } finally {
                conn.disconnect()
            }
            JSONObject(body)
        }.getOrElse { error ->
            synchronized(trafficLock) {
                trafficError = error.message ?: error.javaClass.simpleName
            }
            return
        }
        synchronized(trafficLock) {
            val upload = json.optLong("uploadTotal", lastUploadTotal)
            val download = json.optLong("downloadTotal", lastDownloadTotal)
            val now = System.currentTimeMillis()
            if (lastTrafficAt > 0L && now > lastTrafficAt) {
                val elapsed = (now - lastTrafficAt).coerceAtLeast(1L) / 1000.0
                uploadSpeed = ((upload - lastUploadTotal).coerceAtLeast(0L) / elapsed).toLong()
                downloadSpeed = ((download - lastDownloadTotal).coerceAtLeast(0L) / elapsed).toLong()
            }
            lastTrafficAt = now
            lastUploadTotal = upload
            lastDownloadTotal = download
            trafficError = ""
        }
    }

    private fun describeFailure(fallback: String): String {
        val lines = synchronized(recentLogs) { recentLogs.toList() }
        val cleaned = lines.map { ansiPattern.replace(it, "").trim() }.filter { it.isNotEmpty() }
        val reason = cleaned.firstOrNull { it.contains("FATAL") || it.contains("ERROR") }
            ?: return fallback
        return reason.substringAfter("] ", reason).take(1000)
    }

    private fun waitForReady(process: Process, socksAddr: String) {
        val parsed = parseHostPort(socksAddr) ?: return
        // sing-box 二进制将近 60MB，冷启动光 exec 就可能好几秒，窗口太短会把本来能起来的内核误判为超时。
        val deadline = System.currentTimeMillis() + 30000
        while (System.currentTimeMillis() < deadline) {
            if (!process.isAlive) {
                lastError = describeFailure("sing-box exited before SOCKS listener became ready")
                throw IllegalStateException(lastError)
            }
            runCatching {
                Socket().use { socket ->
                    socket.connect(InetSocketAddress(parsed.first, parsed.second), 250)
                }
            }.onSuccess {
                return
            }
            Thread.sleep(80)
        }
        lastError = describeFailure("timed out waiting for sing-box SOCKS listener $socksAddr")
        throw IllegalStateException(lastError)
    }

    private fun parseHostPort(addr: String): Pair<String, Int>? {
        val idx = addr.lastIndexOf(':')
        if (idx <= 0 || idx == addr.lastIndex) {
            return null
        }
        val host = addr.substring(0, idx).trim().trim('[', ']')
        val port = addr.substring(idx + 1).toIntOrNull() ?: return null
        return host to port
    }

    // 内核日志只进 recentLogs 供失败诊断使用，不再逐行推给界面：
    // sing-box 一秒能刷几十行，灌进连接日志会把真正的运行事件顶掉。
    // 管道仍必须读干，否则 stdout 缓冲区满了内核会被阻塞住。
    private fun pumpLogs(process: Process) {
        Thread({
            runCatching {
                process.inputStream.bufferedReader().useLines { lines ->
                    lines.forEach { line ->
                        if (line.isNotBlank()) {
                            synchronized(recentLogs) {
                                if (recentLogs.size >= 60) {
                                    recentLogs.removeFirst()
                                }
                                recentLogs.addLast(line)
                            }
                        }
                    }
                }
            }
        }, "safelink-sing-box-log").start()
    }

    private fun monitor(process: Process) {
        Thread({
            val exitCode = runCatching { process.waitFor() }.getOrDefault(-1)
            var unexpected = false
            synchronized(lock) {
                if (this.process === process) {
                    this.process = null
                    val state = if (exitCode == 0) "stopped" else "error"
                    if (exitCode != 0 && lastError.isEmpty()) {
                        lastError = "sing-box exited with code $exitCode"
                    }
                    EventBus.emit("proxy.status", statusLocked(state))
                    unexpected = true
                }
            }
            // 主动 stop 时 stopLocked 已经把 process 置空，走不到这里，只有真的崩了才回调。
            if (unexpected) {
                runCatching { exitListener?.invoke() }
            }
        }, "safelink-sing-box-monitor").start()
    }
}
