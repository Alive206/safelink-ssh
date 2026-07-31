package com.safelink.ssh

import android.content.Intent
import android.net.VpnService
import android.os.Build
import java.io.File
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.MethodChannel
import mobilecore.Mobilecore

class MainActivity : FlutterActivity() {
    private var pendingVpnPrepare: MethodChannel.Result? = null
    private lateinit var secureStore: SecureJsonStore
    private val core by lazy { CoreHolder.get(this) }

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
                    "proxyStatus" -> result.success(SingBoxRuntime.status())
                    "secureRead" -> {
                        val key = call.argument<String>("key")
                        result.success(if (key == null) null else secureStore.read(key))
                    }
                    "secureWrite" -> {
                        val key = call.argument<String>("key")
                        val value = call.argument<String>("value") ?: ""
                        if (key == null) {
                            result.error("bad_request", "secureWrite requires key", null)
                        } else {
                            secureStore.write(key, value)
                            result.success(true)
                        }
                    }
                    "secureRemove" -> {
                        val key = call.argument<String>("key")
                        if (key != null) {
                            secureStore.remove(key)
                        }
                        result.success(true)
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
                            try {
                                result.success(core.call(method, enrichCorePayload(payload)))
                            } catch (error: Throwable) {
                                result.error("core_error", error.message, null)
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
        return json.toString()
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
