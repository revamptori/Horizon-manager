package com.horizon.servermanager

internal object Core {
    init { System.loadLibrary("horizon") }
    external fun nativeInit(directory: String): String
    external fun call(request: String): String
    external fun shutdown()
}
