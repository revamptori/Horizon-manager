package com.horizon.servermanager

import android.view.ViewGroup
import android.webkit.WebView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

@RunWith(AndroidJUnit4::class)
class HostSmokeTest {
    @Test fun goCoreReportsItsActualAndroidCapabilities() {
        val capabilities = JSONObject(Core.call("{\"method\":\"host_capabilities\",\"params\":{}}"))
        assertFalse(capabilities.has("error"))
        assertEquals("android", capabilities.getJSONObject("result").getString("platform"))
        assertFalse(capabilities.getJSONObject("result").getBoolean("hostingAvailable"))
        val start = JSONObject(Core.call("{\"method\":\"start_server\",\"params\":{\"serverId\":\"smoke\",\"directory\":\"/tmp\",\"memoryMb\":512}}"))
        assertTrue(start.getString("error").contains("Android JVM"))
        val escape = JSONObject(Core.call("{\"method\":\"prepare_server_directory\",\"params\":{\"directory\":\"/sdcard/outside-horizon\"}}"))
        assertTrue(escape.has("error"))
    }
    @Test fun packagedWebUiConnectsToJniAndShowsRuntimeBoundary() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            val deadline = System.currentTimeMillis() + 25_000
            var content = ""
            while (System.currentTimeMillis() < deadline) {
                val ready = CountDownLatch(1)
                val result = AtomicReference("")
                scenario.onActivity { activity ->
                    val view = activity.findViewById<ViewGroup>(android.R.id.content).getChildAt(0) as WebView
                    assertFalse(view.settings.allowFileAccess)
                    view.evaluateJavascript("document.body.innerText", { text -> result.set(text); ready.countDown() })
                }
                assertTrue("WebView did not answer", ready.await(5, TimeUnit.SECONDS))
                content = result.get()
                if (content.contains("Android runtime not included")) break
                Thread.sleep(250)
            }
            assertTrue("Packaged UI did not initialize: $content", content.contains("Android runtime not included"))
            assertFalse(content.contains("Could not connect to the local host"))
        }
    }
}
