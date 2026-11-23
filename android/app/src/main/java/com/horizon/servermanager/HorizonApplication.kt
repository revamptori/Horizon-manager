package com.horizon.servermanager

import android.app.Application
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import java.util.concurrent.Executors
import org.json.JSONObject

class HorizonApplication : Application() {
    var initializationError: String? = null
        private set
    private val executor = Executors.newSingleThreadExecutor()
    override fun onCreate() {
        super.onCreate()
        try {
            val response = JSONObject(Core.nativeInit(filesDir.absolutePath))
            if (response.has("error")) initializationError = response.getString("error")
        } catch (error: Exception) { initializationError = error.message ?: "Go core initialization failed" }
        catch (error: UnsatisfiedLinkError) { initializationError = "This device ABI is not supported by the bundled Go library." }
        if (initializationError == null) {
            val manager = getSystemService(ConnectivityManager::class.java)
            manager.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
                private fun changed() { executor.execute { Core.call("{\"method\":\"network_changed\",\"params\":{}}") } }
                override fun onAvailable(network: Network) = changed()
                override fun onLost(network: Network) = changed()
                override fun onLinkPropertiesChanged(network: Network, properties: LinkProperties) = changed()
            })
        }
    }
}
