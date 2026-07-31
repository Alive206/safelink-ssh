package com.safelink.ssh

import android.content.Context
import mobilecore.Core
import mobilecore.Mobilecore

/**
 * Holds the single process-wide mobilecore.Core instance.
 *
 * MainActivity (Flutter bridge) and SafeLinkVpnService (VPN worker) live in the
 * same process and must share one Core, otherwise VPN state started by the
 * service is invisible to vpnStatus polls coming from the Dart side.
 */
object CoreHolder {
    @Volatile
    private var core: Core? = null

    fun get(context: Context): Core {
        core?.let { return it }
        return synchronized(this) {
            core ?: Mobilecore.newCore(context.applicationContext.filesDir.absolutePath)
                .also { core = it }
        }
    }
}
