# R8 shrinks, optimizes and obfuscates the app's Java (the names a reverse
# engineer would read when unpacking the APK). What must stay by its real
# name is kept here; everything else of ours is renamed.

# The gomobile bridge: Go registers these classes' native methods by their
# full name, and Go calls back into them, so they must not be renamed or
# removed.
-keep class app.albatross.mobile.** { *; }
-keep class go.** { *; }
-keepclasseswithmembernames,includedescriptorclasses class * {
    native <methods>;
}

# Bridge implements the gomobile Platform interface; Go calls its methods
# from native code (the engine's callbacks), so keep them.
-keep class * implements app.albatross.mobile.Platform { *; }

# The Android components named in the manifest are kept by R8 already; the
# VPN service is kept with its members because the system binds to it.
-keep class app.albatross.vpn.TunnelService { *; }

# Standard safe keeps.
-keepattributes *Annotation*,Signature,InnerClasses,EnclosingMethod
-keepclassmembers enum * {
    public static **[] values();
    public static ** valueOf(java.lang.String);
}
-keepclassmembers class * implements android.os.Parcelable {
    public static final ** CREATOR;
}
-dontwarn app.albatross.mobile.**
-dontwarn go.**
