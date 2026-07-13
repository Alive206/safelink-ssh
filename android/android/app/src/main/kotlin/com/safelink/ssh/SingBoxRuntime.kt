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

    fun start(
        context: Context,
        configJson: String,
        node: String,
        proxyMode: String,
        socksAddr: String,
        clashAddr: String,
    ): Map<String, Any?> {
        synchronized(lock) {
            stopLocked(emitEvent = false)
            val appContext = context.applicationContext
            val workDir = File(appContext.filesDir, "sing-box").also { it.mkdirs() }
            val configFile = File(workDir, "config.json")
            configFile.writeText(configJson, Charsets.UTF_8)

            val binary = File(appContext.applicationInfo.nativeLibraryDir, "libsing-box.so")
            if (!binary.isFile) {
                lastError = "sing-box binary not found: ${binary.absolutePath}"
                throw IllegalStateException(lastError)
            }
            binary.setExecutable(true, false)

            try {
                runCheck(binary, configFile, workDir)

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
                lastTrafficAt = 0L
                lastUploadTotal = 0L
                lastDownloadTotal = 0L
                uploadSpeed = 0L
                downloadSpeed = 0L

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
        synchronized(lock) {
            val current = process
            val state = if (current != null && current.isAlive) "running" else {
                process = null
                "stopped"
            }
            return statusLocked(state)
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
        lastTrafficAt = 0L
        lastUploadTotal = 0L
        lastDownloadTotal = 0L
        uploadSpeed = 0L
        downloadSpeed = 0L
        if (emitEvent) {
            EventBus.emit("proxy.status", statusLocked("stopped"))
        }
    }

    private fun statusLocked(state: String): Map<String, Any?> {
        val traffic = if (state == "running") queryTraffic() else null
        return mapOf(
            "state" to state,
            "node" to nodeName,
            "mode" to mode,
            "started_at" to startedAt,
            "last_error" to lastError,
            "upload_total_bytes" to (traffic?.first ?: lastUploadTotal),
            "download_total_bytes" to (traffic?.second ?: lastDownloadTotal),
            "upload_speed_bps" to uploadSpeed,
            "download_speed_bps" to downloadSpeed,
        )
    }

    private fun queryTraffic(): Pair<Long, Long>? {
        val addr = clashAPIAddr
        if (addr.isBlank()) return null
        return runCatching {
            val conn = URL("http://$addr/connections").openConnection() as HttpURLConnection
            conn.connectTimeout = 700
            conn.readTimeout = 700
            conn.requestMethod = "GET"
            val body = conn.inputStream.bufferedReader().use { it.readText() }
            conn.disconnect()
            val json = JSONObject(body)
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
            upload to download
        }.getOrNull()
    }

    private fun runCheck(binary: File, configFile: File, workDir: File) {
        val check = ProcessBuilder(binary.absolutePath, "check", "-c", configFile.absolutePath)
            .directory(workDir)
            .redirectErrorStream(true)
            .start()
        if (!check.waitFor(8, TimeUnit.SECONDS)) {
            check.destroyForcibly()
            lastError = "sing-box config check timed out"
            throw IllegalStateException(lastError)
        }
        val output = check.inputStream.bufferedReader().readText().trim()
        if (check.exitValue() != 0) {
            lastError = if (output.isEmpty()) {
                "sing-box config check failed"
            } else {
                output.take(4000)
            }
            throw IllegalStateException(lastError)
        }
    }

    private fun waitForReady(process: Process, socksAddr: String) {
        val parsed = parseHostPort(socksAddr) ?: return
        val deadline = System.currentTimeMillis() + 5000
        while (System.currentTimeMillis() < deadline) {
            if (!process.isAlive) {
                lastError = "sing-box exited before SOCKS listener became ready"
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
        lastError = "timed out waiting for sing-box SOCKS listener $socksAddr"
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

    private fun pumpLogs(process: Process) {
        Thread({
            runCatching {
                process.inputStream.bufferedReader().useLines { lines ->
                    lines.forEach { line ->
                        if (line.isNotBlank()) {
                            EventBus.emit("proxy.log", mapOf("line" to line.take(1000)))
                        }
                    }
                }
            }
        }, "safelink-sing-box-log").start()
    }

    private fun monitor(process: Process) {
        Thread({
            val exitCode = runCatching { process.waitFor() }.getOrDefault(-1)
            synchronized(lock) {
                if (this.process !== process) {
                    return@synchronized
                }
                this.process = null
                val state = if (exitCode == 0) "stopped" else "error"
                if (exitCode != 0 && lastError.isEmpty()) {
                    lastError = "sing-box exited with code $exitCode"
                }
                EventBus.emit("proxy.status", statusLocked(state))
            }
        }, "safelink-sing-box-monitor").start()
    }
}
