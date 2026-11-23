plugins { id("com.android.application"); id("org.jetbrains.kotlin.android") }

android {
    namespace = "com.horizon.servermanager"
    compileSdk = 35
    ndkVersion = "28.0.13004108"
    defaultConfig {
        applicationId = "com.horizon.servermanager"
        minSdk = 26
        targetSdk = 35
        versionCode = 2
        versionName = "0.2.0-go-preview.1"
        ndk { abiFilters += listOf("arm64-v8a", "x86_64") }
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    kotlinOptions { jvmTarget = "17" }
    buildFeatures { buildConfig = true }
    buildTypes {
        release { isMinifyEnabled = false }
        debug { isDebuggable = true }
    }
    packaging { jniLibs { useLegacyPackaging = false } }
    lint { abortOnError = true }
}

dependencies {
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test:core:1.6.1")
}
