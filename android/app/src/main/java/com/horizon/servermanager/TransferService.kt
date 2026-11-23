package com.horizon.servermanager

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import org.json.JSONObject
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors

/** Only real downloads/archive jobs run here. This is not a Minecraft JVM service. */
class TransferService : Service() {
    private val executor = Executors.newSingleThreadExecutor()
    private val handler = Handler(Looper.getMainLooper())
    private var active = 0
    private var wakeLock: PowerManager.WakeLock? = null

    override fun onBind(intent: Intent?): IBinder? = null
    override fun onCreate() {
        super.onCreate()
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "Horizon file operations", NotificationManager.IMPORTANCE_LOW)
        )
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val notification = Notification.Builder(this, CHANNEL).setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Horizon file operation in progress")
            .setContentText("Downloading verified files or processing a local backup. Minecraft hosting is not running.")
            .setOngoing(true).setContentIntent(open).build()
        if (Build.VERSION.SDK_INT >= 29) startForeground(1, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        else startForeground(1, notification)
        wakeLock = getSystemService(PowerManager::class.java).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "Horizon:FileOperation").apply { setReferenceCounted(false); acquire(20 * 60 * 1000L) }
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val key = intent?.getStringExtra("workId")
        val work = if (key == null) null else pending.remove(key)
        if (work == null) { if (active == 0) finish(); return START_NOT_STICKY }
        active++
        executor.execute {
            val response = try { Core.call(work.request) } catch (error: Exception) { failure(error.message ?: "Native file operation failed") }
            work.complete(response)
            handler.post { active--; if (active == 0) finish() }
        }
        return START_NOT_STICKY
    }
    private fun finish() { releaseWakeLock(); stopForeground(STOP_FOREGROUND_REMOVE); stopSelf() }
    private fun releaseWakeLock() { wakeLock?.let { if (it.isHeld) it.release() }; wakeLock = null }
    override fun onTimeout(startId: Int, fgsType: Int) {
        Core.shutdown() // Cancels Go HTTP transfers and archive operations before the OS service deadline.
        finish()
    }
    override fun onDestroy() {
        if (active > 0) Core.shutdown()
        releaseWakeLock(); executor.shutdown(); super.onDestroy()
    }
    private data class Work(val request: String, val complete: (String) -> Unit)
    companion object {
        private const val CHANNEL = "horizon-transfers"
        private val pending = ConcurrentHashMap<String, Work>()
        fun failure(message: String): String = JSONObject().put("error", message).toString()
        fun enqueue(context: Context, request: String, complete: (String) -> Unit) {
            if (pending.size >= 8) { complete(failure("Too many pending file operations")); return }
            val key = UUID.randomUUID().toString()
            pending[key] = Work(request, complete)
            try { context.startForegroundService(Intent(context, TransferService::class.java).putExtra("workId", key)) }
            catch (error: Exception) { pending.remove(key); complete(failure("Could not start the foreground file service: ${error.message}")) }
        }
    }
}
