package com.horizon.servermanager

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.TextView
import java.io.ByteArrayInputStream
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import org.json.JSONObject

class MainActivity : Activity() {
    private lateinit var view: WebView
    @Volatile private var disposed = false
    private val executor = ThreadPoolExecutor(2, 4, 30L, TimeUnit.SECONDS, ArrayBlockingQueue(64))
    private val heavy = setOf("install_vanilla_server", "create_backup", "restore_backup")
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val error = (application as HorizonApplication).initializationError
        if (error != null) { setContentView(TextView(this).apply { text = "Horizon could not start: $error"; setPadding(32, 64, 32, 32) }); return }
        window.statusBarColor = Color.rgb(16, 17, 19)
        window.navigationBarColor = Color.rgb(16, 17, 19)
        view = WebView(this)
        view.setBackgroundColor(Color.rgb(16, 17, 19))
        view.settings.apply {
            javaScriptEnabled = true; domStorageEnabled = true
            allowFileAccess = false; allowContentAccess = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            javaScriptCanOpenWindowsAutomatically = false
            setSupportMultipleWindows(false)
            cacheMode = WebSettings.LOAD_NO_CACHE
            safeBrowsingEnabled = true
        }
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG)
        view.addJavascriptInterface(Bridge(), "HorizonAndroid")
        view.webChromeClient = WebChromeClient()
        view.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(webView: WebView?, request: WebResourceRequest): WebResourceResponse = asset(request.url)
            override fun shouldOverrideUrlLoading(webView: WebView?, request: WebResourceRequest): Boolean {
                if (trusted(request.url)) return false
                if (request.url.scheme == "https" && request.hasGesture()) {
                    try { startActivity(Intent(Intent.ACTION_VIEW, request.url)) } catch (_: Exception) { /* No external browser installed. */ }
                }
                return true
            }
            override fun onPageStarted(webView: WebView, url: String?, icon: android.graphics.Bitmap?) {
                if (url == null || !trusted(Uri.parse(url))) webView.stopLoading()
            }
        }
        setContentView(view)
        view.loadUrl("https://horizon.local/")
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 10)
        }
    }
    private fun trusted(uri: Uri) = uri.scheme == "https" && uri.host == "horizon.local" && (uri.port == -1 || uri.port == 443) && uri.userInfo == null
    private fun asset(uri: Uri): WebResourceResponse {
        fun denied() = WebResourceResponse("text/plain", "UTF-8", 403, "Forbidden", emptyMap(), ByteArrayInputStream("Only packaged Horizon assets may load in this WebView".toByteArray()))
        if (!trusted(uri)) return denied()
        val path = uri.path ?: "/"
        if (!Regex("^/[a-zA-Z0-9/_.-]*$").matches(path) || path.split('/').any { it == ".." || it == "." }) return denied()
        val headers = mapOf(
            "Cache-Control" to "no-store", "X-Content-Type-Options" to "nosniff",
            "Content-Security-Policy" to "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'"
        )
        if (path == "/host.js") return WebResourceResponse("text/javascript", "UTF-8", 200, "OK", headers, ByteArrayInputStream("window.__HORIZON_HOST__='android';".toByteArray()))
        val name = if (path == "/") "index.html" else path.removePrefix("/")
        val mime = when (name.substringAfterLast('.', "")) {
            "html" -> "text/html"; "js" -> "text/javascript"; "css" -> "text/css"; "woff2" -> "font/woff2"; "woff" -> "font/woff"; "svg" -> "image/svg+xml"; "png" -> "image/png"; else -> "application/octet-stream"
        }
        return try { WebResourceResponse(mime, if (mime.startsWith("text/")) "UTF-8" else null, 200, "OK", headers, assets.open("web/$name")) }
        catch (_: java.io.IOException) { WebResourceResponse("text/plain", "UTF-8", 404, "Not Found", headers, ByteArrayInputStream(byteArrayOf())) }
    }
    inner class Bridge {
        @JavascriptInterface fun post(serialized: String) {
            if (disposed || serialized.length > 2 * 1024 * 1024) return
            var id = ""
            try {
                val request = JSONObject(serialized); id = request.getString("id")
                require(Regex("^[a-zA-Z0-9-]{1,64}$").matches(id))
                val method = request.getString("method")
                val command = JSONObject().put("method", method).put("params", request.optJSONObject("params") ?: JSONObject()).toString()
                val requestId = id
                val complete: (String) -> Unit = { response -> deliver(requestId, response) }
                if (method in heavy) TransferService.enqueue(this@MainActivity, command, complete)
                else executor.execute { try { complete(Core.call(command)) } catch (error: Exception) { complete(TransferService.failure(error.message ?: "Native call failed")) } }
            } catch (error: Exception) { if (id.isNotEmpty()) deliver(id, TransferService.failure(error.message ?: "Invalid native request")) }
        }
    }
    private fun deliver(id: String, response: String) {
        runOnUiThread { if (!disposed && ::view.isInitialized) view.evaluateJavascript("window.__horizonResolve && window.__horizonResolve(${JSONObject.quote(id)},${JSONObject.quote(response)});", null) }
    }
    override fun onDestroy() {
        disposed = true
        if (::view.isInitialized) { view.removeJavascriptInterface("HorizonAndroid"); view.destroy() }
        executor.shutdown(); super.onDestroy()
    }
}
