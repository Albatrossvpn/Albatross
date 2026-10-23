// The Go engine comes in as app/libs/albatross.aar (gomobile bind) and the
// cores as app/src/main/jniLibs/<abi>/lib*.so; build/android/build.sh puts
// both there and then runs this build.
plugins {
    id("com.android.application")
}

val appVersion = (System.getenv("VERSION") ?: "0.1.0-dev")
// versionCode must grow with every release: 0.1.2 -> 102, 1.4.0 -> 10400.
val appVersionCode = Regex("""^(\d+)\.(\d+)\.(\d+)""").find(appVersion)?.destructured
    ?.let { (a, b, c) -> a.toInt() * 10000 + b.toInt() * 100 + c.toInt() } ?: 1

android {
    namespace = "app.albatross.vpn"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.albatross.vpn"
        minSdk = 24
        targetSdk = 35
        versionCode = maxOf(appVersionCode, 1)
        versionName = appVersion
    }

    // A stable key lets new versions install over old ones. CI passes it
    // through these variables; without them the debug key is used.
    val keystore = System.getenv("ALBATROSS_KEYSTORE")
    signingConfigs {
        if (keystore != null && file(keystore).exists()) {
            create("release") {
                storeFile = file(keystore)
                storePassword = System.getenv("ALBATROSS_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ALBATROSS_KEY_ALIAS")
                keyPassword = System.getenv("ALBATROSS_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }

    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a", "x86_64")
            isUniversalApk = false
        }
    }

    packaging {
        // The cores are executables shipped as libraries; Android only lets
        // an app run them when they are extracted to the library folder.
        jniLibs {
            useLegacyPackaging = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation(files("libs/albatross.aar"))
}
