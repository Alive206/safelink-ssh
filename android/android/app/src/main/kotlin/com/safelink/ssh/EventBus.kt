package com.safelink.ssh

import android.os.Handler
import android.os.Looper
import io.flutter.plugin.common.EventChannel

object EventBus {
    private val mainHandler = Handler(Looper.getMainLooper())
    private var sink: EventChannel.EventSink? = null

    fun attach(eventSink: EventChannel.EventSink?) {
        mainHandler.post {
            sink = eventSink
        }
    }

    fun emit(type: String, payload: Map<String, Any?> = emptyMap()) {
        val event = HashMap<String, Any?>()
        event["type"] = type
        event["payload"] = payload
        mainHandler.post {
            sink?.success(event)
        }
    }
}
